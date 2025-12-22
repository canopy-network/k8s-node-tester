# Genesis File Generator

A Go script that generates genesis files, config files, keystores, and node identities for multi-chain Canopy network deployments.

## Usage

```bash
cd go-scripts/genesis-generator/cmd/genesis
go run main.go -config <config-name>
```

**Examples:**
```bash
# Use default config
go run main.go

# Use specific config
go run main.go -config max

# Use custom paths
go run main.go -config default -path /path/to/configs -output /path/to/output
```

### Command-Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | `default` | Name of the config to use |
| `-path` | `../../` | Path to the folder containing the config files |
| `-output` | `../../artifacts` | Path to the folder where the output files will be saved |

## Configuration

### configs.yml

Configuration is defined in `configs.yml` (located at `go-scripts/genesis-generator/configs.yml`). Each named config (e.g., `max`, `medium`, `default`) contains:

```yaml
default:
  general:
    concurrency: 100          # Number of concurrent goroutines for key generation
    password: "pablito"       # Password for keystore encryption
    buffer: 1000              # Buffer size for internal channels
    netAddressSuffix: ".p2p"  # Suffix appended to netAddress in genesis.json
    jsonBeautify: true        # If true, beautifies json files with indentation
  # Total node entries including multi-committee validator expansions
  nodes:
    count: 4  # Validators count once per committee they participate in
  # Individual chain configuration
  chains:
    chain_1:
      id: 1                   # Unique chain ID
      rootChain: 1            # Root chain ID (can be itself for root chains)
      sleepUntil: 60          # Optional: seconds to add to current time for sleepUntil epoch
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
        - id: 2                             # Committee ID (typically another chain's ID)
          repeatedIdentityValidatorCount: 1 # Validators that appear in BOTH chains' genesis (creates expanded entries)
          repeatedIdentityDelegatorCount: 0 # Delegators that appear in BOTH chains' genesis
          validatorCount: 0                 # Validators staked for this committee but ONLY appear in native chain's genesis
          delegatorCount: 0                 # Delegators staked for this committee but ONLY appear in native chain's genesis
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

### Node Count Calculation

The `nodes.count` field must equal the total number of entries in `ids.json`, which includes:
- All regular validators
- All full nodes
- RepeatedIdentity expansions (one per additional committee)
- Committee-only validators (these are NEW validators)
- Delegators do NOT count (they're not physical nodes)

**Example calculation for the default config:**
- Chain_1: 3 regular validators + 0 full nodes + 1 repeatedIdentity expansion + 1 committee-only validator = 5
- Chain_2: 1 validator + 0 full nodes = 1
- **Total: 6**

### Committee Assignments

Validators and delegators are automatically assigned to their own chain's committee (using the chain's ID). The `committees` field allows assigning validators/delegators to **additional** committees on other chains.

There are two types of committee assignments:

1. **RepeatedIdentity** (`repeatedIdentityValidatorCount`, `repeatedIdentityDelegatorCount`):
   - **Reuses existing validators** from the chain's validator pool
   - Validators/delegators appear in **both** chains' genesis files
   - Create **multiple entries** in `ids.json` (one per committee, with different IDs)
   - In native chain genesis: `committees: [own_chain, target_chain]`
   - In target chain genesis: `committees: [target_chain]`
   - Used for validators that need to be physical nodes on both chains

2. **Committee-Only** (`validatorCount`, `delegatorCount`):
   - Creates **NEW validators** staked **only** for the target committee
   - Appear in the **root chain's genesis validators** with `committees: [target_committee]`
   - Do **not** appear in the target chain's genesis validators (nested chains have empty validators)
   - **Accounts and keystore** are in the **target chain**, not the root chain
   - Have **one entry** in `ids.json` with `chainId` = target committee ID
   - Create **additional entries** that count towards `nodes.count`

**Example:**
```yaml
chain_1:
  id: 1
  validators:
    count: 3
  committees:
    - id: 2
      repeatedIdentityValidatorCount: 1  # 1 existing validator appears in both chains
      repeatedIdentityDelegatorCount: 0
      validatorCount: 1                   # 1 NEW validator staked for committee 2 in chain_1
      delegatorCount: 0
```

This means:
- 3 regular validators are created for chain_1 (staked for committee 1)
- 1 additional committee-only validator is created in **chain_1** (staked only for committee 2)
- 1st regular validator: participates in committee 1 AND committee 2, appears in **both** genesis files (repeatedIdentity)
- 2nd & 3rd regular validators: only participate in committee 1
- Committee-only validator: appears in **chain_1's genesis** with `committees: [2]`, NOT in chain_2's genesis

**Node Count:**
- `nodes.count` = validators + fullNodes + repeatedIdentity expansions + committee-only validators
- For this example: 3 + 0 + 1 + 1 = 5

**RepeatedIdentity validators:**
- Appear in **both** chains' genesis files
- In the root chain genesis: `committees: [1, 2]`
- In the nested chain genesis: `committees: [2]`
- Have **multiple entries** in `ids.json` (one per committee, with different IDs)

**Committee-only validators:**
- Appear in the **root chain's genesis validators** with `committees: [target_committee]`
- Nested chains have **empty validators** in their genesis
- **Accounts and keystore** are in the **target chain** (not root chain)
- Have **one entry** in `ids.json` with `chainId` = target committee ID

### Validation

The script validates:
1. The sum of validators + full nodes + repeatedIdentity expansions + committee-only validators equals `nodes.count`
2. At least one root chain has validators (for rootChainNode assignment)
3. RepeatedIdentity assignment counts don't exceed available validators/delegators (committee-only creates NEW validators, so no limit)
4. Committee IDs reference valid chain IDs

**peerNode Assignment:**
- Validators with root chain identity: peerNode is themselves
- Validators without root chain identity: peerNode is assigned to repeatedIdentity validators if available, otherwise falls back to root chain validators

### Delegators

Delegators are staked entities that delegate to validators but are **not physical servers**:
- They do **not** count towards `nodes.count`
- They do **not** have `netAddress` in genesis.json
- They do **not** appear in ids.json (only validators and full nodes are included)
- They use **negative IDs** internally (-1, -2, -3, ...) to avoid gaps in the positive ID sequence used by validators and full nodes
- Multi-committee delegators get unique expanded negative IDs (continuing from the lowest base delegator ID)
- In keystore.json, delegators use nicknames like `delegator-1`, `delegator-2`, etc. (using the absolute value of their negative ID)

### Chain Types

- **Root Chain**: A chain where `rootChain` equals its own `id`
- **Nested Chain**: A chain where `rootChain` points to another chain's `id`

### accounts.yml

Optional file (`go-scripts/genesis-generator/accounts.yml`) defining main accounts that are shared across all chains.

**Format:**
```yaml
accounts:
  <account-name>:
    address: "<hex-encoded address>"
    publicKey: "<hex-encoded public key>"
    privateKey: "<hex-encoded private key>"
```

**Example:**
```yaml
accounts:
  main-account:
    address: "851e90eaef1fa27debaee2c2591503bdeec1d123"
    publicKey: "b88a5928e54cbf0a36e0b98f5bcf02de9a9a1deba..."
    privateKey: "6c275055a4f6ae6bccf1e6552e172c7b8cc538a7..."
  faucet:
    address: "f96bc4553a5c6ef2506a2260d2562d4db282e879"
    publicKey: "a8053eb0cb6d69b292a508dba9af0bcf4be5f4d9..."
    privateKey: "44609f49a53983d11792e83833f3c390742139864..."
```

Each account will be added to:
- `ids.json` under `main-accounts` map
- Each chain's genesis accounts (with that chain's configured account amount)
- Each chain's keystore (with the account name as nickname)

These accounts are **not** associated with any validator, delegator, or full node.

If the file doesn't exist or is empty, no main accounts will be generated.

## Output Structure

Output files are generated in `artifacts/{config-name}/`:

```
artifacts/
└── {config-name}/
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

Contains all validator and full node identities in a map structure (delegators are not included), plus the main accounts. **RepeatedIdentity** multi-committee validators appear multiple times with different IDs (one per committee). Non-repeatedIdentity multi-committee validators appear only once:

```json
{
  "main-accounts": {
    "main-account": {
      "address": "a1b2c3d4e5f6789012345678901234567890abcd",
      "publicKey": "c99b7821e54cbf0a36e0b98f5bcf02de9a9a1deba...",
      "privateKey": "7d385055a4f6ae6bccf1e6552e172c7b8cc538a7...",
      "password": "your_password"
    },
    "faucet": {
      "address": "b2c3d4e5f67890123456789012345678901234ef",
      "publicKey": "d88b6829f65dcf1b47f1c99f6cdf13ef0b0b2efcb...",
      "privateKey": "8e386166b5g7bf7ccd2f7663e283d8c9ddc649b8...",
      "password": "your_password"
    }
  },
  "keys": {
    "node-1": {
      "id": 1,
      "chainId": 1,
      "rootChainId": 1,
      "rootChainNode": 1,
      "peerNode": 1,
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
      "peerNode": 2,
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
      "peerNode": 4,
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
      "peerNode": 4,
      "address": "851e90eaef1fa27debaee2c2591503bdeec1d123",
      "publicKey": "b88a5928e54cbf0a36e0b98f5bcf02de9a9a1deba...",
      "privateKey": "6c275055a4f6ae6bccf1e6552e172c7b8cc538a7...",
      "nodeType": "validator"
    }
  }
}
```

### Main Accounts

The `main-accounts` map contains accounts defined in `accounts.yml` (see [accounts.yml](#accountsyml) section). These accounts:
- Have the **same identity** (address, keys) across all chains
- Do **not** have chain-specific fields (`chainId`, `rootChainId`, `rootChainNode`, `peerNode`)
- Are added to each chain's genesis accounts and keystore

**Notes:**
- `node-1` and `node-4` have the same keys but different IDs - this is a **repeatedIdentity** multi-committee validator appearing once for each committee
- `node-4` has `rootChainNode: 1` because it's the same identity as `node-1` (repeatedIdentity multi-committee)
- `node-3` has `rootChainNode: 1` because it's a native nested chain node assigned to a root chain node (round-robin distribution)
- `node-4` has `peerNode: 4` (itself) because it has a root chain identity
- `node-3` has `peerNode: 4` because it's a nested chain node without root chain identity, assigned to a peer node that does have root chain identity

**Node Types:** `validator`, `fullnode`

**rootChainNode Logic** (validators and full nodes only, delegators don't have this field):
- **Root chain validator**: `rootChainNode` = its own ID
- **Nested chain validator (same identity on root chain)**: `rootChainNode` = the ID of its root chain entry
- **Nested chain validator (no root chain identity)**: `rootChainNode` = a root chain validator ID (distributed evenly)

**peerNode Logic** (validators and full nodes only, delegators don't have this field):
- **Root chain validator**: `peerNode` = its own ID
- **Nested chain validator (same identity on root chain)**: `peerNode` = its own ID
- **Nested chain validator (no root chain identity)**: `peerNode` = ID of a nested chain validator that has root chain identity (distributed evenly)
- **Root chain full node**: `peerNode` = ID of a root chain validator (distributed evenly)
- **Nested chain full node**: `peerNode` = ID of a nested chain validator that has root chain identity (distributed evenly)

### config.json

Node configuration with placeholders for dynamic values:

```json
{
  "chainId": 1,
  "rootChain": [
    {
      "chainId": 1,
      "url": "ROOT_NODE_ID"
    }
  ],
  "externalAddress": "NODE_ID",
  "listenAddress": "0.0.0.0:9001",
  "dialPeers": [],
  "sleepUntil": 1734567890
}
```

**Placeholders:**
- `NODE_ID` - Replace with the node's `id` from ids.json
- `ROOT_NODE_ID` - Replace with a root chain node's `id`

**Optional Fields:**
- `sleepUntil` - Unix epoch timestamp. If `sleepUntil` is set in the chain config (in seconds), this field will contain `time.Now() + sleepUntil` as an epoch. The node will sleep until this time before starting. Omitted if not configured.

**Root vs Nested Chain Config:**

For **root chains** (chain is its own root):
```json
"rootChain": [
  { "chainId": 1, "url": "ROOT_NODE_ID" }
]
```

For **nested chains** (different root chain):
```json
"rootChain": [
  { "chainId": 2, "url": "NODE_ID" },
  { "chainId": 1, "url": "ROOT_NODE_ID" }
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
| `max` | Large scale deployment (475 nodes) |
| `medium` | Medium scale deployment (100 nodes) |
| `default` | Standard deployment with 2 chains (4 entries) |
| `min` | Minimal single-chain setup (2 nodes) |
| `mature` | Production-like setup (200 nodes) |

## Adding Custom Configs

Add a new entry to `configs.yml`:

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

**Note:** `nodes.count` must equal validators + full nodes + repeatedIdentity expansions + committee-only validators. Delegators don't create entries.

Then run:
```bash
go run main.go -config my_custom
```
