# k8s-node-tester

This repository provisions and operates a K3s-based Kubernetes cluster for canopy node testing, then
generates and applies per-chain configuration artifacts (genesis, keystore, accounts, delegators,
committees) into the cluster as ConfigMaps. It also includes a workflow for installing Helm, TLS via
Hetzner DNS, and a monitoring stack (Prometheus, Grafana, Loki, Promtail). An Ansible-based setup
can create/add nodes, update the cluster, and configure TLS/monitoring end-to-end.

## Pre-requisites

### Server

- Debian-based OS (e.g., Ubuntu)
- Non-root user with sudo privileges
- SSH access (public key authentication)
- [Firewall allowances](https://docs.k3s.io/installation/requirements#inbound-rules-for-k3s-nodes):
  - Inbound TCP 22 from your IP (SSH)
  - Inbound TCP 80 from 0.0.0.0/0 (HTTP for apps/Let’s Encrypt)
  - Inbound TCP 443 from 0.0.0.0/0 (HTTPS for apps)
  - Inbound TCP 6443 between cluster nodes (K3s API server)
  - Inbound TCP 10250 between cluster nodes (Kubelet API)
  - Inbound UDP 8472 between cluster nodes (Flannel VXLAN)
  - Inbound TCP 2379-2380 for embedded etcd (required for HA embedded etcd)
  - Optional: Inbound TCP 6443 from your local IP (kubectl access)

### DNS

- Domain using Hetzner DNS nameservers
- Wildcard A record pointing to the cluster's public IPs (e.g., A *.example.com -> 192.168.0.1, 192.168.0.2)
- Hetzner read/write API token (for cert-manager DNS01)

### Local/remote machine (controller)

- Required packages: `make`, `kubectl`, `helm`, `ansible`, `cilium`, `go` (to build the tools)
- With Homebrew installed, the others can be installed via:
  - `make helpers/brew-install-requirements`
- SSH access from this machine to all cluster servers

### Alerting  
- [Discord webhook URL](https://support.discord.com/hc/en-us/articles/228383668-Intro-to-Webhooks) (for Grafana alerts)

## Repository structure (key parts)

- `Makefile` — primary entry point for workflows:
  - Ansible orchestration (setup, TLS, monitoring)
  - Helm-based canopy workload management
- `go-scripts/`
  - `genesis-generator` — CLI to generate chain artifacts under `artifacts/<CONFIG>/chain_<id>/...`
    plus shared `ids.json`
  - `cmd/k8s-applier` — CLI to apply the generated artifacts to Kubernetes as ConfigMaps
  - `init-node` — auxiliary node init program
- `ansible/` — inventory, example secrets, playbooks, and collection requirements
- `cluster/` — TLS and monitoring setup scripts, and Helm chart for `canopy`

## First time setup

1. Clone the repository:

   ```bash
   git clone https://github.com/canopy-network/k8s-node-tester.git
   cd k8s-node-tester
   ```

2. Prepare Ansible inventory:

   ```bash
   cp ansible/inventory.example.yml ansible/inventory.yml
   # edit ansible/inventory.yml with server/agent hosts and SSH users
   ```

3. Prepare TLS and domain secrets:

   ```bash
   cp ansible/secrets.example.yml ansible/secrets.yml
   # edit domain, email, hetzner api token; optionally set k3s token
   ```

4. Run the full cluster setup (installs requirements, creates/updates nodes, TLS, monitoring):

   ```bash
   make ansible/cluster-setup
   ```

    - This will:
      - Install k3s server and agent nodes
      - Install ansible requirements
      - Run the base site playbook
      - Install Helm
      - Configure TLS with Hetzner (uses `ansible/secrets.yml`)
      - Install monitoring (Prometheus, Grafana, Loki, Promtail)

5. To later add a new node, update `ansible/inventory.yml` and run:

   ```bash
   make ansible/site
   ```

6. Build the [Go](https://go.dev/doc/install) tools (required for config generation/apply):

   ```bash
   make go-scripts/build
   ```

Note: Manual provisioning instead of Ansible can be done if desired. See [Makefile](Makefile) targets
in the `manual setup` section for manual workflows.

## Configuration profiles

Profiles live in:

- [`./go-scripts/genesis-generator/configs.yaml`](./go-scripts/genesis-generator/configs.yaml)

They define:

- Global generation parameters (e.g., concurrency, JSON formatting)
- Node count
- Per-chain composition (validators, full nodes, accounts, delegators, committees)

Available profiles include `default`, `min`, `medium`, `max`, and `mature`. Check the script's
[`README`](./go-scripts/genesis-generator/cmd/genesis/README.md) file for details.

## Testing workflow

1. Ensure `kubectl` is targeting the cluster:

   ```bash
   kubectl config use-context k3s-ansible
   ```

2. Generate and apply artifacts for a given profile:

   ```bash
   make test/prepare CONFIG=default
   ```

   - `CONFIG` is the name of the profile to use from the profiles set in the genesis `configs.yaml`
     file.
3. Start canopy workloads:

   ```bash
   make test/start NODES=4
   ```

   - Must Set `NODES` to match or align with the config's `nodes.count` in the selected profile for
     correct scaling.

## Optional: Network chaos (Chaos Mesh)

Install Chaos Mesh (one-time):

```bash
make chaos/mesh

# or manually:
helm repo add chaos-mesh https://charts.chaos-mesh.org
helm repo update
helm upgrade --install chaos-mesh chaos-mesh/chaos-mesh -n chaos-mesh --create-namespace \
  -f ./cluster/chaos-mesh/values.yaml
```

Then configure `networkChaos` in `cluster/canopy/helm/values.yaml`. You can define multiple
faults at once via `networkChaos.experiments`. Each experiment renders a separate
NetworkChaos resource, or a Schedule resource if `schedule` is set for periodic runs.
By default the selector targets only the canopy pods (`app=node`).

Example:

```yaml
networkChaos:
  enabled: true
  experiments:
    - name: canopy-latency
      action: delay
      delay:
        latency: "150ms"
        jitter: "20ms"
        correlation: "25"
    - name: canopy-loss
      action: loss
      loss:
        loss: "2"
        correlation: "0"
    - name: canopy-egress-blackhole
      action: loss
      mode: random-max-percent
      value: "30"
      direction: to
      duration: "5s"
      schedule: "@every 1m"
      loss:
        loss: "100"
        correlation: "0"
```

Cleanup

- Remove Helm release and ConfigMaps used by tests:

  ```bash
  make test/destroy
  ```

- Tear down the entire K3s cluster via Ansible:

  ```bash
  make ansible/teardown
  ```
