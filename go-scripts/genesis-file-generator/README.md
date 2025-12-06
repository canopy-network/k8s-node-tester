# Genesis File Generator

A Go tool to generate genesis files, validator keys, and configurations for Canopy blockchain nodes.

## Usage

```bash
go run main.go <config-file.yaml>
```

Or use the Makefile targets:

```bash
make minimal  # 10 accounts, 5 validators, 10 delegators (multi-node)
make mature   # 1M accounts, 10K validators, 10K delegators
make full     # 10M accounts, 100K validators, 100K delegators
make max      # 100M accounts, 1M validators, 1M delegators
```

## Configuration File

Create a YAML config file with the following parameters:

```yaml
# Number of accounts, validators, and delegators to generate
accounts: 10
validators: 5
delegators: 10

# Password for the keystore encryption
password: "mysecurepass"

# Multi-node mode: generates config files for each validator
multi_node: true

# Performance tuning
concurrency: 100  # Number of concurrent goroutines for key generation
buffer: 1000      # Channel buffer size

# Optional: Custom URLs (only used when multi_node is false)
# root_chain_url: "http://custom-node:50002"
# external_address: "custom-node"
```

### Configuration Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `accounts` | Number of accounts to generate | 100 |
| `validators` | Number of validators to generate | 5 |
| `delegators` | Number of delegators to generate | 10 |
| `password` | Keystore encryption password | "pablito" |
| `multi_node` | Generate config for each validator | false |
| `concurrency` | Concurrent goroutines for key gen | 100 |
| `buffer` | Channel buffer size | 1000 |
| `root_chain_url` | Custom root chain URL (single node only) | - |
| `external_address` | Custom external address (single node only) | - |

## Output

Generated files are placed in the `.config/` directory:

- `genesis.json` - The genesis file with all validators and accounts
- `accounts.json` - Intermediate accounts file
- `validator-N/` - Per-validator directories (when `multi_node: true`):
  - `genesis.json` - Copy of the genesis file
  - `config.json` - Node configuration
  - `keystore.json` - Encrypted validator key
  - `validator_key.json` - Validator private key

## Examples

### Single Node Setup

```yaml
# config-single.yaml
accounts: 1000
validators: 1
delegators: 10
password: "mysecurepass"
multi_node: false
concurrency: 50
buffer: 500
root_chain_url: "http://my-node:50002"
external_address: "my-node"
```

```bash
go run main.go config-single.yaml
```

### Multi-Node Cluster

```yaml
# config-cluster.yaml
accounts: 100
validators: 5
delegators: 20
password: "clusterpass"
multi_node: true
concurrency: 100
buffer: 1000
```

```bash
go run main.go config-cluster.yaml
```

This will generate separate configuration directories for each validator (`validator-0`, `validator-1`, etc.) ready to be deployed to different nodes.
