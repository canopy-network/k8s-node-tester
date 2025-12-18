package main

// k8s-applier reads canopy chain configuration files and applies them to kubernetes as configmaps,
// then creates load balancer services for each chain.
// It scans chain-specific genesis, keystore, and config files, along with a shared ids file,
// validates chain folder naming (chain_<number>), and creates or updates configmaps in the specified namespace.
// After configmaps are applied, it creates a LoadBalancer service for each chain (rpc-lb-{chainID})
// that selects pods with matching chain ID labels and routes to the RPC port.
// All configuration files are created by the genesis-generator tool and configuration is controlled via flags

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	configFileExt = ".json"    // extension of the config files
	genesisFile   = "genesis"  // genesis file name
	keystoreFile  = "keystore" // keystore file name
	configFile    = "config"   // config file name
	idsFile       = "ids"      // ids file name

	chainIdLabel = "canopy/chain-id" // pod label for the chain id, required to make chain ID service targets
	portName     = "rpc"             // name for the rpc service port
	rpcPort      = 50002             // port for the rpc service
)

var (
	path       = flag.String("path", "../../artifacts", "path to the folders containing the config files")
	config     = flag.String("config", "default", "folder name of the specific config")
	namespace  = flag.String("namespace", "canopy", "namespace to create configmaps in")
	kubeconfig = flag.String("kubeconfig", filepath.Join(os.Getenv("HOME"), ".kube", "config"), "path to kubeconfig")
	timeout    = flag.Duration("timeout", 30*time.Second, "timeout for operations")
	startPort  = flag.Int("startPort", 1000, "start port range for the services")

	// validates chain folder name format as in chain_<number>
	chainRegex = regexp.MustCompile(`^chain_(\d+)$`)
)

// Keys is the map of node keys
type Keys struct {
	Keys map[string]NodeKey `json:"keys"`
}

// NodeKey is an excerpt of the node key information in order to initialize the node in the go-scripts/init-node script
type NodeKey struct {
	Id      int `json:"id"`
	ChainID int `json:"chainId"`
}

func main() {
	// parse flags
	flag.Parse()
	// create default logger
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	// context with termination handler
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	log.Info("building configs for chains")
	// check if config exists and is a valid directory
	configPath := filepath.Join(*path, *config)
	stat, err := os.Stat(configPath)
	if err != nil {
		log.Error("failed to find config",
			slog.String("err", err.Error()), slog.String("path", configPath))
		os.Exit(1)
	}
	if !stat.IsDir() {
		log.Error("config is not a directory", slog.String("path", configPath))
		os.Exit(1)
	}
	// retrieve and validate chain folders
	folders, err := getChainFolders(configPath)
	if err != nil {
		log.Error("failed to get chain folders",
			slog.String("err", err.Error()), slog.String("path", configPath))
		os.Exit(1)
	}
	// sort folders alphabetically for deterministic order
	sort.Strings(folders)
	if len(folders) == 0 {
		log.Warn("no chain folders found", slog.String("path", configPath))
		os.Exit(0)
	}
	// create clientset to interact with Kubernetes API
	clientset, err := buildClientSet(*kubeconfig)
	if err != nil {
		log.Error("failed to build clientset",
			slog.String("err", err.Error()), slog.String("kubeconfig", *kubeconfig))
		os.Exit(1)
	}
	// build data maps, then configmaps
	dataByType, err := buildDataMaps(filepath.Join(*path, *config), []string{genesisFile,
		keystoreFile, configFile}, configFileExt, idsFile, folders)
	if err != nil {
		log.Error("failed to build data maps", slog.String("err", err.Error()))
		os.Exit(1)
	}
	// build ConfigMaps from data maps
	configMaps := buildConfigMapsFromData(*namespace, dataByType)
	// apply ConfigMaps
	for _, configmap := range configMaps {
		err := applyConfigMap(ctx, clientset, *namespace, configmap.Name, configmap)
		if err != nil {
			log.Error("failed to ensure configmap",
				slog.String("err", err.Error()), slog.String("kubeconfig", *kubeconfig))
			os.Exit(1)
		}
		log.Info("applied configmap", slog.String("name", configmap.Name), slog.Int("keys", len(configmap.Data)))
	}
	// parse the ids file
	var keys Keys
	if err := json.Unmarshal([]byte(dataByType[idsFile][idsFile+configFileExt]), &keys); err != nil {
		log.Error("failed to parse ids file",
			slog.String("err", err.Error()))
		os.Exit(1)
	}
	// get the chains
	chains := getChains(&keys)
	// create the service
	if err := createServices(ctx, *namespace, *startPort, clientset, chains); err != nil {
		log.Error("failed to create services",
			slog.String("err", err.Error()))
		os.Exit(1)
	}
	log.Info("configs applied", slog.Int("chains", len(folders)))
}

// buildDataMaps reads JSON files and builds the per-file-type data maps:
// dataByType[fileType][key] = contents
func buildDataMaps(basePath string, fileTypes []string, ext string, idsFile string, folders []string) (
	map[string]map[string]string, error) {
	dataByType := map[string]map[string]string{}
	// initialize maps for each file type
	for _, ft := range fileTypes {
		dataByType[ft] = map[string]string{}
	}
	// aggregate chain-specific files into each file type map
	for fileType, files := range dataByType {
		for _, chain := range folders {
			// get the chain ID
			chainID, err := getChainID(chain)
			if err != nil {
				return nil, fmt.Errorf("get chain ID: %w", err)
			}
			// retrieve the file
			path := filepath.Join(basePath, chain, fileType+ext)
			contents, err := readJSONFile(path)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", path, err)
			}
			files[buildEntryKey(fileType, chainID, ext)] = string(contents)
		}
	}
	// add ids.json (not per-chain)
	idsPath := filepath.Join(basePath, idsFile+ext)
	idsContents, err := readJSONFile(idsPath)
	if err != nil {
		return nil, fmt.Errorf("build configmaps: %w", err)
	}
	// store under its own fileType entry
	dataByType[idsFile] = map[string]string{
		idsFile + ext: string(idsContents),
	}
	return dataByType, nil
}

// getChainFolders returns a list of valid chain folders in the given path
func getChainFolders(configPath string) (folders []string, err error) {
	files, err := os.ReadDir(configPath)
	if err != nil {
		return nil, fmt.Errorf("obtain chain folders: %w", err)
	}
	for _, file := range files {
		if file.IsDir() && chainRegex.MatchString(file.Name()) {
			folders = append(folders, file.Name())
		}
	}
	return folders, nil
}

// buildClientSet creates a Kubernetes clientset from the given kubeconfig
func buildClientSet(kubeconfig string) (*kubernetes.Clientset, error) {
	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("build config: %w", err)
	}
	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}
	return clientset, nil
}

// buildConfigMapsFromData is an util to create config maps from the given data
func buildConfigMapsFromData(namespace string, dataByType map[string]map[string]string) []*corev1.ConfigMap {
	cms := make([]*corev1.ConfigMap, 0, len(dataByType))
	for fileType, data := range dataByType {
		if len(data) == 0 {
			continue
		}
		cms = append(cms, createConfigMap(fileType, namespace, data))
	}
	return cms
}

// createConfigMap is a helper function to create an in-memory config map
func createConfigMap(name, namespace string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
	}
}

// getChainID is a helper function to retrieve the chain ID from a chain name
func getChainID(chain string) (int, error) {
	m := chainRegex.FindStringSubmatch(chain)
	if m == nil {
		return 0, fmt.Errorf("invalid chain name: %s", chain)
	}
	id, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("convert chain ID to int: %w", err)
	}
	return id, nil
}

// buildEntryKey is a helper function to build a key for a config map entry
func buildEntryKey(fileName string, chainID int, ext string) string {
	return fmt.Sprintf("%s_%d%s", fileName, chainID, ext)
}

// readJSONFile reads a JSON file, unmarshals into any, and returns re-indented bytes.
func readJSONFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file [path: %s]: %w", path, err)
	}
	// unmarshal into a generic interface
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("invalid JSON [path: %s]: %w", path, err)
	}
	// marshal back out with indentation (2 spaces)
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("format JSON [path: %s]: %w", path, err)
	}
	return pretty, nil
}

// applyConfigMap creates the configmap or updates it if it already exists.
func applyConfigMap(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string,
	configMap *corev1.ConfigMap) error {
	cmClient := clientset.CoreV1().ConfigMaps(namespace)
	_, err := cmClient.Create(ctx, configMap, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create ConfigMap %s/%s: %w", namespace, name, err)
	}
	// the configmap already exists, will try to update it
	existing, err := cmClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get ConfigMap %s/%s: %w", namespace, name, err)
	}
	// overwrite data (this replaces the Data map entirely).
	existing.Data = configMap.Data
	_, err = cmClient.Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update ConfigMap %s/%s: %w", namespace, name, err)
	}
	return nil
}

// getChains iterates over the ids file and returns a map of chainID->nodes
func getChains(nodes *Keys) []int {
	chains := make([]int, 0)
	for _, node := range nodes.Keys {
		if slices.Contains(chains, node.ChainID) {
			continue
		}
		chains = append(chains, node.ChainID)
	}
	return chains
}

// createServices creates a load balancer service for each chain to use
func createServices(ctx context.Context, namespace string, startPort int, clientset *kubernetes.Clientset, chains []int) error {
	for _, chainID := range chains {
		serviceName := fmt.Sprintf("rpc-lb-chain-%d", chainID)
		port := int32(startPort + chainID)
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      serviceName,
				Namespace: namespace,
				Labels: map[string]string{
					"type": "chain",
				},
			},
			Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer,
				Selector: map[string]string{
					"app":        "node",
					chainIdLabel: strconv.Itoa(chainID),
				},
				Ports: []corev1.ServicePort{
					{
						Name:       portName,
						Port:       port,
						TargetPort: intstr.FromInt(rpcPort),
					},
				},
			},
		}
		_, err := clientset.CoreV1().Services(namespace).Create(ctx, service, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("service creation %s: %w", serviceName, err)
		}
	}
	return nil
}
