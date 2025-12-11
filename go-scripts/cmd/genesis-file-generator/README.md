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
  general:
    concurrency: 100          # Number of concurrent goroutines for key generation
    password: "pablito"       # Password for keystore encryption
    buffer: 1000              # Buffer size for internal channels
    netAddressSuffix: ".p2p"  # Suffix appended to netAddress in genesis.json
  # Total amount of nodes (validators + delegators + full nodes across all chains)
  nodes:
    count: 3
  # Individual chain configuration
  chains:
    # Identifier of the chain so other chains can reference it
    chain_1:
      id: 1                   # Unique chain ID
      rootChain: 1            # Root chain ID (can be itself for root chains)
      # Validators: nodes providing consensus
      validators:
        count: 2
        stakedAmount: 1000000000
        amount: 1000000       # Account balance
        committees: [1, 2]
      # Full nodes: nodes providing full services without being part of consensus
      fullNodes:
        count: 0
        amount: 1000000
      # Accounts: regular accounts without node infrastructure
      accounts:
        count: 1
        amount: 1000000
      # Delegators: staked nodes that delegate to validators
      delegators:
        count: 0
        stakedAmount: 1000000000
        amount: 1000000
        committees: [1, 2]
    chain_2:
      id: 2
      rootChain: 1            # Nested chain (chain_1 is root)
      validators:
        count: 1
        stakedAmount: 1000000000
        amount: 1000000
        committees: [2]
      fullNodes:
        count: 0
        amount: 1000000
      accounts:
        count: 2
        amount: 1000000
      delegators:
        count: 0
        stakedAmount: 1000000000
        amount: 1000000
        committees: [2]
```

### Validation

The script validates that the sum of all validators, delegators, and full nodes across all chains equals the `nodes.count` value before running. If there's a mismatch, the script will exit with an error.

### Chain Types

- **Root Chain**: A chain where `rootChain` equals its own `id`
- **Nested Chain**: A chain where `rootChain` points to another chain's `id`

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
| `max` | Large scale deployment (500 nodes) |
| `medium` | Medium scale deployment (100 nodes) |
| `default` | Standard deployment with 2 chains (3 nodes) |
| `min` | Minimal single-chain setup (2 nodes) |
| `mature` | Production-like setup (300 nodes) |

## Adding Custom Configs

Add a new entry to `configs.yaml`:

```yaml
my_custom:
  general:
    concurrency: 50
    password: "mypassword"
    buffer: 1000
    netAddressSuffix: ".p2p"
  nodes:
    count: 10
  chains:
    my_chain:
      id: 1
      rootChain: 1
      validators:
        count: 5
        stakedAmount: 500000000
        amount: 1000000
        committees: [1]
      fullNodes:
        count: 3
        amount: 1000000
      accounts:
        count: 50
        amount: 1000000
      delegators:
        count: 2
        stakedAmount: 500000000
        amount: 1000000
        committees: [1]
```

**Note:** Ensure `nodes.count` (10) equals the sum of validators (5) + delegators (2) + full nodes (3) = 10.

Then run:
```bash
go run main.go my_custom
```
