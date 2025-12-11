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
    jsonBeautify: true        # If true, beautifies json files with indentation
  # Total physical nodes (validators + full nodes, NOT delegators)
  nodes:
    count: 3  # Delegators don't count as physical nodes
  # Individual chain configuration
  chains:
    chain_1:
      id: 1                   # Unique chain ID
      rootChain: 1            # Root chain ID (can be itself for root chains)
      validators:
        count: 2
        stakedAmount: 1000000000
        amount: 1000000       # Account balance
      fullNodes:
        count: 0
        amount: 1000000
      accounts:
        count: 1
        amount: 1000000
      delegators:
        count: 0
        stakedAmount: 1000000000
        amount: 1000000
      # Cross-chain committee assignments
      committees:
        - id: 2               # Committee ID (typically another chain's ID)
          validatorCount: 1   # Number of validators to assign to this committee
          delegatorCount: 0   # Number of delegators to assign to this committee
    chain_2:
      id: 2
      rootChain: 1            # Nested chain (chain_1 is root)
      validators:
        count: 1
        stakedAmount: 1000000000
        amount: 1000000
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
      committees: []          # No cross-chain assignments
```

### Committee Assignments

Validators and delegators are automatically assigned to their own chain's committee (using the chain's ID). The `committees` field allows assigning validators/delegators to **additional** committees on other chains.

**Example:**
```yaml
chain_1:
  id: 1
  validators:
    count: 2
  committees:
    - id: 2
      validatorCount: 1
      delegatorCount: 0
```

This means:
- 2 validators are created for chain_1
- 1 validator will participate in **both** committee 1 (its own chain) AND committee 2
- 1 validator will only participate in committee 1

**Multi-committee validators:**
- Appear in **both** chains' genesis files
- In the root chain genesis: `committees: [1, 2]`
- In the nested chain genesis: `committees: [2]`
- Have **multiple entries** in `ids.json` (one per committee, with different IDs)

### Validation

The script validates:
1. The sum of all validators and full nodes equals `nodes.count` (delegators don't count as physical nodes)
2. At least one root chain has validators (for rootChainNode assignment)
3. Committee assignment counts don't exceed available validators/delegators
4. Committee IDs reference valid chain IDs

### Delegators

Delegators are staked entities that delegate to validators but are **not physical servers**:
- They do **not** count towards `nodes.count`
- They do **not** have `netAddress` in genesis.json
- They do **not** have `rootChainNode` in ids.json

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

Contains all node identities in a map structure. Multi-committee validators appear multiple times with different IDs:

```json
{
  "keys": {
    "node-1": {
      "id": 1,
      "chainId": 1,
      "rootChainId": 1,
      "rootChainNode": 1,
      "address": "851e90eaef1fa27debaee2c2591503bdeec1d123",
      "publicKey": "b88a5928e54cbf0a36e0b98f5bcf02de9a9a1deba...",
      "privateKey": "6c275055a4f6ae6bccf1e6552e172c7b8cc538a7...",
      "nodeType": "validator"
    },
    "node-2": {
      "id": 2,
      "chainId": 1,
      "rootChainId": 1,
      "rootChainNode": 2,
      "address": "...",
      "publicKey": "...",
      "privateKey": "...",
      "nodeType": "validator"
    },
    "node-3": {
      "id": 3,
      "chainId": 2,
      "rootChainId": 1,
      "rootChainNode": 1,
      "address": "f333c1a6af3cc044b192f0423e2f415451f97d1d",
      "publicKey": "...",
      "privateKey": "...",
      "nodeType": "validator"
    },
    "node-4": {
      "id": 4,
      "chainId": 2,
      "rootChainId": 1,
      "rootChainNode": 1,
      "address": "851e90eaef1fa27debaee2c2591503bdeec1d123",
      "publicKey": "b88a5928e54cbf0a36e0b98f5bcf02de9a9a1deba...",
      "privateKey": "6c275055a4f6ae6bccf1e6552e172c7b8cc538a7...",
      "nodeType": "validator"
    }
  }
}
```

**Notes:**
- `node-1` and `node-4` have the same keys but different IDs - this is a multi-committee validator appearing once for each committee
- `node-4` has `rootChainNode: 1` because it's the same identity as `node-1` (multi-committee)
- `node-3` has `rootChainNode: 1` because it's a native nested chain node assigned to a root chain node (round-robin distribution)

**Node Types:** `validator`, `delegator`, `fullnode`

**rootChainNode Logic** (validators and full nodes only, delegators don't have this field):
- **Root chain validator**: `rootChainNode` = its own ID
- **Nested chain validator (same identity on root chain)**: `rootChainNode` = the ID of its root chain entry
- **Nested chain validator (no root chain identity)**: `rootChainNode` = a root chain validator ID (distributed evenly)

### config.json

Node configuration with wildcards for dynamic values:

```json
{
  "chainId": 1,
  "rootChain": [
    {
      "chainId": 1,
      "url": "http://node-|ROOT_NODE_ID|:50002"
    }
  ],
  "externalAddress": "node-|NODE_ID|",
  "listenAddress": "0.0.0.0:9001",
  "dialPeers": ["|DIAL_PEER|"]
}
```

**Wildcards:**
- `|NODE_ID|` - Replace with the node's `id` from ids.json
- `|ROOT_NODE_ID|` - Replace with a root chain node's `id`
- `|DIAL_PEER|` - Replace with peer address to dial

**Root vs Nested Chain Config:**

For **root chains** (chain is its own root):
```json
"rootChain": [
  { "chainId": 1, "url": "http://node-|ROOT_NODE_ID|:50002" }
]
```

For **nested chains** (different root chain):
```json
"rootChain": [
  { "chainId": 2, "url": "http://node-|NODE_ID|:50002" },
  { "chainId": 1, "url": "http://node-|ROOT_NODE_ID|:50002" }
]
```

### genesis.json

Chain genesis file containing validators, accounts, and parameters. Validators from other chains that participate in this chain's committee are included with only this chain's committee in their committees list.

### keystore.json

Encrypted private keys for all nodes participating in the chain, including:
- Native validators, delegators, and full nodes
- Cross-chain validators/delegators from other chains that participate in this chain's committee

Nicknames follow the pattern `node-{id}`.

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
    jsonBeautify: false
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
      committees: []
```

**Note:** Ensure `nodes.count` (10) equals the sum of validators (5) + delegators (2) + full nodes (3) = 10.

Then run:
```bash
go run main.go my_custom
```
