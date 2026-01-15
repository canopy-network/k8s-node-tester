package main

// init-node is a Kubernetes init container script that prepares canopy node configuration files.
// It reads the pod's hostname to determine its index, looks up the corresponding node key from an ids.json file,
// then copies and configures the appropriate genesis, keystore, config, and validator_key files for that specific node.
// The script unmarshals the config file into a Config struct and programmatically modifies it by setting root chain URLs,
// external addresses, and dial peers based on the node's chain configuration and peer information.
// After modification, the config is marshaled back to JSON and written to /root/.canopy for the main canopy container to use.
// Finally, the script uses the Kubernetes API to label the pod with its chain ID for service targeting.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	configPath    = "/root/configs" // path where the config files are stored
	canopyPath    = "/root/.canopy" // path where the canopy files are stored
	configFileExt = ".json"         // extension of the config files
	idsFile       = "ids"           // file containing the keys for the node
	genesisFile   = "genesis"       // file containing the genesis data for the node
	configFile    = "config"        // file containing the config data for the node
	keystoreFile  = "keystore"      // file containing the keystore data for the node
	validatorFile = "validator_key" // file containing the validator data for the node

	serviceSuffix = ".p2p" // suffix for the service name in order for the node to be discoverable

	configFilePerms = 0644              // writable file permissions [readable by everyone, writable by owner]
	chainIdLabel    = "canopy/chain-id" // pod label for the chain id, required to make chain ID service targets
)

// Keys is the map of node keys
type Keys struct {
	Keys map[string]NodeKey `json:"keys"`
}

// NodeKey is the structure representing the node key information in order to initialize the node
type NodeKey struct {
	Id            int    `json:"id"`
	ChainID       int    `json:"chainId"`
	RootChainID   int    `json:"rootChainId"`
	RootChainNode int    `json:"rootChainNode"`
	PeerNode      int    `json:"peerNode"`
	Address       string `json:"address"`
	PublicKey     string `json:"publicKey"`
	PrivateKey    string `json:"privateKey"`
	NodeType      string `json:"nodeType"`
}

func main() {
	// create a default logger
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	// cancellable context
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// obtain the pod index from the hostname
	hostname, podPrefix, podId, err := getPodId()
	if err != nil {
		log.Error("failed to get pod index", slog.String("err", err.Error()))
		os.Exit(1)
	}
	log.Info("starting config setup for pod", slog.Int("podId", podId))
	// open the ids file
	idsFile, err := os.Open(fullFilePath(configPath, idsFile, configFileExt))
	if err != nil {
		log.Error("failed to open keys file", slog.String("err", err.Error()))
		os.Exit(1)
	}
	defer idsFile.Close()
	// load the nodes file into memory
	var nodes Keys
	err = json.NewDecoder(idsFile).Decode(&nodes)
	if err != nil {
		log.Error("failed to decode keys file", slog.String("err", err.Error()))
		os.Exit(1)
	}
	// get the node key for the pod index
	node, ok := nodes.Keys[hostname]
	if !ok {
		log.Error("node key not found for hostname", slog.String("hostname", hostname))
		os.Exit(1)
	}
	// sanity check the pod index
	if podId != node.Id {
		log.Error("pod index does not match node key index",
			slog.Int("podIndex", podId), slog.Int("nodeKeyId", node.Id))
		os.Exit(1)
	}
	// copy the genesis file to the canopy directory
	src := fullFilePath(configPath, indexedFileName(genesisFile, node.ChainID), configFileExt)
	dst := fullFilePath(canopyPath, genesisFile, configFileExt)
	err = copy(src, dst)
	if err != nil {
		log.Error("failed to copy genesis file",
			slog.String("err", err.Error()),
			slog.String("src", src),
			slog.String("dst", dst))
		os.Exit(1)
	}
	// copy the keystore file to the canopy directory
	src = fullFilePath(configPath, indexedFileName(keystoreFile, node.ChainID), configFileExt)
	dst = fullFilePath(canopyPath, keystoreFile, configFileExt)
	err = copy(src, dst)
	if err != nil {
		log.Error("failed to copy keystore file",
			slog.String("err", err.Error()),
			slog.String("src", src),
			slog.String("dst", dst))
		os.Exit(1)
	}
	// open the config file and parse it to perform substitutions
	src = fullFilePath(configPath, indexedFileName(configFile, node.ChainID), configFileExt)
	rawConfig, err := os.ReadFile(src)
	if err != nil {
		log.Error("failed to read config file", slog.String("err", err.Error()), slog.String("src", src))
		os.Exit(1)
	}
	var config Config
	err = json.Unmarshal(rawConfig, &config)
	if err != nil {
		log.Error("failed to unmarshal config file", slog.String("err", err.Error()), slog.String("src", src))
		os.Exit(1)
	}
	// obtain the root node full key by splitting the hostname by "-" and obtaining the identifier
	rootNodeKey := fmt.Sprintf("%s%d", podPrefix, node.RootChainNode)
	rootNode, ok := nodes.Keys[rootNodeKey]
	if !ok {
		log.Error("failed to find root node", slog.String("rootNodeKey", rootNodeKey))
		os.Exit(1)
	}
	// do the same for the peer node
	peerNodeKey := fmt.Sprintf("%s%d", podPrefix, node.PeerNode)
	peerNode, ok := nodes.Keys[peerNodeKey]
	if !ok {
		log.Error("failed to find peer node", slog.String("peerNodeKey", peerNodeKey))
		os.Exit(1)
	}
	// perform the substitutions
	modifyConfig(&config, podPrefix, &node, &rootNode, &peerNode)
	// encode to save it as a file
	rawConfig, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		log.Error("failed to encode config", slog.String("err", err.Error()))
		os.Exit(1)
	}
	// copy the config file to the canopy's directory
	dst = fullFilePath(canopyPath, configFile, configFileExt)
	if err := os.WriteFile(dst,
		rawConfig, configFilePerms); err != nil {
		log.Error("failed to copy config file", slog.String("err", err.Error()), slog.String("dst", dst))
		os.Exit(1)
	}
	// write the validator key file to the canopy's directory
	validatorKeyFile := fmt.Sprintf("\"%s\"", node.PrivateKey)
	dst = fullFilePath(canopyPath, validatorFile, configFileExt)
	if err := os.WriteFile(dst,
		[]byte(validatorKeyFile), configFilePerms); err != nil {
		log.Error("failed to copy validator key file", slog.String("err", err.Error()),
			slog.String("dst", dst))
		os.Exit(1)
	}
	// get the clientset for the current cluster
	clientSet, err := getClientSet()
	if err != nil {
		log.Error("failed to get clientset", slog.String("err", err.Error()))
		os.Exit(1)
	}
	// apply the chain label to the pod
	if err := applyChainLabel(ctx, clientSet, hostname, node.ChainID); err != nil {
		log.Error("failed to apply chain label", slog.String("err", err.Error()))
		os.Exit(1)
	}
	log.Info("finished setting up the config for the node " + hostname)
}

// getPodId returns the hostname, prefix and id of the pod
func getPodId() (hostname, prefix string, id int, err error) {
	separator := "-"
	hostname, err = os.Hostname()
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to get hostname: %w", err)
	}
	parts := strings.Split(hostname, separator)
	if len(parts) < 2 {
		return "", "", 0, fmt.Errorf("invalid hostname format: %s", hostname)
	}
	id, err = strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid pod id: %w", err)
	}
	return hostname, parts[0] + separator, id, nil
}

// fullFilePath returns the full path to the file with the given name and extension
// in the given path
func fullFilePath(path, name, extension string) string {
	return filepath.Join(path, name+extension)
}

// indexedFileName returns a formatted filename with an index suffix (e.g., "config_1")
func indexedFileName(name string, id int) string {
	return fmt.Sprintf("%s_%d", name, id)
}

// copy copies the file from src to dst
func copy(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// modifyConfig applies the config modifications for the specific node
func modifyConfig(config *Config, nodePrefix string, node, rootNode, peerNode *NodeKey) {
	// modify the node id for the root and nested chain
	for idx := range config.RootChain {
		chain := &config.RootChain[idx]
		// default for "NODE_ID"
		chainNode := node
		if chain.URL == "ROOT_NODE_ID" {
			chainNode = rootNode
		}
		chain.URL = buildNodeAddress(true, nodePrefix, chainNode, ":50002")
	}
	// change the external address to itself so it can be discovered by the network
	config.ExternalAddress = buildNodeAddress(false, nodePrefix, node, "")
	// a node should not connect to itself
	var peerToDial string
	if peerNode.Id != node.Id {
		// update the peer address to the peer node
		peerToDial = fmt.Sprintf("%s@tcp://%s%d%s", peerNode.PublicKey, nodePrefix, peerNode.Id, serviceSuffix)
	}
	// Randomize current dial peers
	rand.Shuffle(len(config.DialPeers), func(i, j int) {
		config.DialPeers[i], config.DialPeers[j] = config.DialPeers[j], config.DialPeers[i]
	})
	// keep up to maxOutbound peers on the dial peers list
	config.DialPeers = config.DialPeers[:min(config.MaxOutbound, len(config.DialPeers))]
	// add the peer to the dial peers list
	config.DialPeers = append(config.DialPeers, peerToDial)

}

func buildNodeAddress(http bool, nodePrefix string, node *NodeKey, port string) string {
	httpPrefix := "http://"
	if !http {
		httpPrefix = ""
	}
	return fmt.Sprintf("%s%s%d%s%s", httpPrefix, nodePrefix, node.Id, serviceSuffix, port)
}

// applyChainLabel applies a label to the pod to indicate the chain ID
func applyChainLabel(ctx context.Context, clientset *kubernetes.Clientset, podName string, chainID int) error {
	// get current namespace, requires to be supplied by the pod
	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		return fmt.Errorf("namespace not found")
	}
	// get the pod
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		fmt.Printf("get pod: %v\n", err)
		os.Exit(1)
	}
	// add label
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}
	pod.Labels[chainIdLabel] = strconv.Itoa(chainID)
	// update pod
	_, err = clientset.CoreV1().Pods(namespace).Update(ctx, pod, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update pod: %v", err)
	}
	return nil
}

// getClientSet returns a Kubernetes clientset for the current cluster
func getClientSet() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	clientConfig, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return clientConfig, nil
}

// Config is an excerpt of the config file with all the required fields
type Config struct {
	AutoUpdate              bool        `json:"autoUpdate"`
	LogLevel                string      `json:"logLevel"`
	ChainID                 int         `json:"chainId"`
	SleepUntil              int         `json:"sleepUntil"`
	RootChain               []RootChain `json:"rootChain"`
	RunVDF                  bool        `json:"runVDF"`
	Headless                bool        `json:"headless"`
	WalletPort              string      `json:"walletPort"`
	ExplorerPort            string      `json:"explorerPort"`
	RPCPort                 string      `json:"rpcPort"`
	AdminPort               string      `json:"adminPort"`
	RPCURL                  string      `json:"rpcURL"`
	AdminRPCURL             string      `json:"adminRPCUrl"`
	TimeoutS                int         `json:"timeoutS"`
	DataDirPath             string      `json:"dataDirPath"`
	DbName                  string      `json:"dbName"`
	InMemory                bool        `json:"inMemory"`
	NetworkID               int         `json:"networkID"`
	ListenAddress           string      `json:"listenAddress"`
	ExternalAddress         string      `json:"externalAddress"`
	MaxInbound              int         `json:"maxInbound"`
	MaxOutbound             int         `json:"maxOutbound"`
	TrustedPeerIDs          any         `json:"trustedPeerIDs"`
	DialPeers               []string    `json:"dialPeers"`
	BannedPeerIDs           any         `json:"bannedPeerIDs"`
	BannedIPs               any         `json:"bannedIPs"`
	GossipThreshold         int         `json:"gossipThreshold"`
	MinimumPeersToStart     int         `json:"minimumPeersToStart"`
	NewHeightTimeoutMS      int         `json:"newHeightTimeoutMS"`
	ElectionTimeoutMS       int         `json:"electionTimeoutMS"`
	ElectionVoteTimeoutMS   int         `json:"electionVoteTimeoutMS"`
	ProposeTimeoutMS        int         `json:"proposeTimeoutMS"`
	ProposeVoteTimeoutMS    int         `json:"proposeVoteTimeoutMS"`
	PrecommitTimeoutMS      int         `json:"precommitTimeoutMS"`
	PrecommitVoteTimeoutMS  int         `json:"precommitVoteTimeoutMS"`
	CommitTimeoutMS         int         `json:"commitTimeoutMS"`
	RoundInterruptTimeoutMS int         `json:"roundInterruptTimeoutMS"`
	MaxTotalBytes           int         `json:"maxTotalBytes"`
	MaxTransactionCount     int         `json:"maxTransactionCount"`
	IndividualMaxTxSize     int         `json:"individualMaxTxSize"`
	DropPercentage          int         `json:"dropPercentage"`
	MetricsEnabled          bool        `json:"metricsEnabled"`
	PrometheusAddress       string      `json:"prometheusAddress"`
}

type RootChain struct {
	ChainID int    `json:"chainId"`
	URL     string `json:"url"`
}
