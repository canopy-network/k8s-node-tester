package main

// init-node is a Kubernetes init container script that prepares canopy node configuration files.
// it reads the pod's hostname to determine its index, looks up the corresponding node key from a keys.json file,
// then copies and configures the appropriate genesis, keystore, config, and validator_key files for that specific node.
// the script performs template substitution in the config file, replacing placeholders like |NODE_ID|, |ROOT_NODE_ID|,
// and |ROOT_NODE_PUBLIC_KEY| with actual values based on the node's chain configuration and root chain node information.
// all configuration files are written to /root/.canopy for the main canopy container to use.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

	configFilePerms = 0644 // writable file permissions [readable by everyone, writable by owner]
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
	// load the keys file into memory
	var keys Keys
	err = json.NewDecoder(idsFile).Decode(&keys)
	if err != nil {
		log.Error("failed to decode keys file", slog.String("err", err.Error()))
		os.Exit(1)
	}
	// get the node key for the pod index
	nodeKey, ok := keys.Keys[hostname]
	if !ok {
		log.Error("node key not found for hostname", slog.String("hostname", hostname))
		os.Exit(1)
	}
	// sanity check the pod index
	if podId != nodeKey.Id {
		log.Error("pod index does not match node key index",
			slog.Int("podIndex", podId), slog.Int("nodeKeyId", nodeKey.Id))
		os.Exit(1)
	}
	// copy the genesis file to the canopy directory
	src := fullFilePath(configPath, indexedFileName(genesisFile, nodeKey.ChainID), configFileExt)
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
	src = fullFilePath(configPath, indexedFileName(keystoreFile, nodeKey.ChainID), configFileExt)
	dst = fullFilePath(canopyPath, keystoreFile, configFileExt)
	err = copy(src, dst)
	if err != nil {
		log.Error("failed to copy keystore file",
			slog.String("err", err.Error()),
			slog.String("src", src),
			slog.String("dst", dst))
		os.Exit(1)
	}
	// open the config file to perform substitutions
	src = fullFilePath(configPath, indexedFileName(configFile, nodeKey.ChainID), configFileExt)
	configFileContents, err := os.ReadFile(src)
	if err != nil {
		log.Error("failed to read config file", slog.String("err", err.Error()), slog.String("src", src))
		os.Exit(1)
	}
	// obtain the root node full key by splitting the hostname by "-" and obtaining the identifier
	rootNodeKey := fmt.Sprintf("%s%d", podPrefix, nodeKey.RootChainNode)
	rootNode, ok := keys.Keys[rootNodeKey]
	if !ok {
		log.Error("failed to find root node", slog.String("rootNodeKey", rootNodeKey))
		os.Exit(1)
	}
	// do the same for the peer node
	peerNodeKey := fmt.Sprintf("%s%d", podPrefix, nodeKey.PeerNode)
	peerNode, ok := keys.Keys[peerNodeKey]
	if !ok {
		log.Error("failed to find peer node", slog.String("peerNodeKey", peerNodeKey))
		os.Exit(1)
	}
	// a peer node should not connect to itself, also, as empty strings are not
	// permitted, the full string is replaced with an empty value
	dialPeer := fmt.Sprintf("\"%s@tcp://node-%s\"", peerNode.PublicKey,
		strconv.Itoa(peerNode.Id)+serviceSuffix)
	if rootNode.Id == peerNode.Id {
		dialPeer = ""
	}
	// perform the substitutions
	configFileContents = performSubstitution(configFileContents, map[string]string{
		"|NODE_ID|":       strconv.Itoa(podId) + serviceSuffix,
		"|ROOT_NODE_ID|":  strconv.Itoa(rootNode.Id) + serviceSuffix,
		"\"|DIAL_PEER|\"": dialPeer,
	})
	// copy the config file to the canopy's directory
	dst = fullFilePath(canopyPath, configFile, configFileExt)
	if err := os.WriteFile(dst,
		configFileContents, configFilePerms); err != nil {
		log.Error("failed to copy config file", slog.String("err", err.Error()), slog.String("dst", dst))
		os.Exit(1)
	}
	// write the validator key file to the canopy's directory
	validatorKeyFile := fmt.Sprintf("\"%s\"", nodeKey.PrivateKey)
	dst = fullFilePath(canopyPath, validatorFile, configFileExt)
	if err := os.WriteFile(dst,
		[]byte(validatorKeyFile), configFilePerms); err != nil {
		log.Error("failed to copy validator key file", slog.String("err", err.Error()),
			slog.String("dst", dst))
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

// performSubstitution replaces all occurrences of keys in the data with their corresponding values
func performSubstitution(data []byte, subs map[string]string) []byte {
	for key, value := range subs {
		data = bytes.ReplaceAll(data, []byte(key), []byte(value))
	}
	return data
}
