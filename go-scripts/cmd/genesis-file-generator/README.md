# Genesis File Generator

A Go script that generates genesis files, config files, keystores, and node identities for multi-chain Canopy network deployments.

## Usage

```bash
cd go-scripts/cmd/genesis-file-generator
go run main.go <config-name>
```

**Example:**
```bash
go run main.go default
```

## Configuration

Configuration is defined in `configs.yaml`. Each named config (e.g., `max`, `medium`, `default`) contains:

```yaml
default:
  password: "pablito"       # Password for keystore encryption
  concurrency: 100          # Number of concurrent goroutines for key generation
  chains:
    chain_1:
      id: 1                 # Unique chain ID
      root_chain: 1         # Root chain ID (self for root chains)
      committees: [1, 2]    # Committees validators are staked to
      delegators: 10
      validators: 5
      full_nodes: 2
      accounts: 100
      staked_amount: 1000000000
    chain_2:
      id: 2
      root_chain: 1         # Nested chain (chain_1 is root)
      committees: [2]
      delegators: 1
      validators: 3
      full_nodes: 5
      accounts: 100
      staked_amount: 1000000000
```

### Chain Types

- **Root Chain**: A chain where `root_chain` equals its own `id`
- **Nested Chain**: A chain where `root_chain` points to another chain's `id`

## Output Structure

```
.config/
├── ids.json              # All node identities across ALL chains
├── chain_1/
│   ├── config.json       # Chain-specific node configuration
│   ├── genesis.json      # Chain genesis file
│   └── keystore.json     # Chain-specific encrypted keys
└── chain_2/
    ├── config.json
    ├── genesis.json
    └── keystore.json
```

## Output Files

### ids.json

Contains all node identities from all chains with unique `idx`:

```json
[
  {
    "idx": 1,
    "chainId": 1,
    "rootChainId": 1,
    "address": "851e90eaef1fa27debaee2c2591503bdeec1d123",
    "publicKey": "b88a5928e54cbf0a36e0b98f5bcf02de9a9a1deba...",
    "privateKey": "6c275055a4f6ae6bccf1e6552e172c7b8cc538a7...",
    "nodeType": "validator"
  },
  {
    "idx": 2,
    "chainId": 1,
    "rootChainId": 1,
    "address": "...",
    "publicKey": "...",
    "privateKey": "...",
    "nodeType": "delegator"
  }
]
```

**Node Types:** `validator`, `delegator`, `fullnode`

### config.json

Node configuration with wildcards for dynamic values:

```json
{
  "chainId": 1,
  "rootChain": [
    {
      "chainId": 1,
      "url": "http://node-{{ROOT_NODE_ID}}:50002"
    }
  ],
  "externalAddress": "node-{{NODE_ID}}",
  "listenAddress": "0.0.0.0:9001"
}
```

**Wildcards:**
- `{{NODE_ID}}` - Replace with the node's `idx` from ids.json
- `{{ROOT_NODE_ID}}` - Replace with a root chain node's `idx`

**Root vs Nested Chain Config:**

For **root chains** (chain is its own root):
```json
"rootChain": [
  { "chainId": 1, "url": "http://node-{{ROOT_NODE_ID}}:50002" }
]
```

For **nested chains** (different root chain):
```json
"rootChain": [
  { "chainId": 2, "url": "http://node-{{NODE_ID}}:50002" },
  { "chainId": 1, "url": "http://node-{{ROOT_NODE_ID}}:50002" }
]
```

### genesis.json

Chain genesis file containing validators, accounts, and parameters.

### keystore.json

Encrypted private keys for all nodes in the chain, with nicknames `node-{idx}`.

## Available Configs

| Config | Description |
|--------|-------------|
| `max` | Large scale deployment |
| `medium` | Medium scale deployment |
| `default` | Standard deployment with 2 chains |
| `min` | Minimal single-chain setup |
| `mature` | Production-like setup |

## Adding Custom Configs

Add a new entry to `configs.yaml`:

```yaml
my_custom:
  password: "mypassword"
  concurrency: 50
  chains:
    my_chain:
      id: 1
      root_chain: 1
      committees: [1]
      delegators: 5
      validators: 3
      full_nodes: 2
      accounts: 50
      staked_amount: 500000000
```

Then run:
```bash
go run main.go my_custom
```

