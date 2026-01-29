define check_vars
	$(foreach var,$1,$(if $(value $(var)),,$(error ERROR: $(var) is not set)))
endef

## help: print each command's help message
.PHONY: help
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' |  sed -e 's/^/ /'

## --- main workflow ---
# ==================================================================================== #
# MAIN WORKFLOW
# ==================================================================================== #

## test/prepare: prepares the genesis config files for the cluster, requires kubectl to have access to the cluster
.PHONY: test/prepare
test/prepare:
	$(MAKE) genesis/generate
	$(MAKE) genesis/apply

## test/start: starts the cluster with the prepared config files
.PHONY: test/start
test/start:
	$(call check_vars, NODES)
	$(eval NAMESPACE ?= canopy)
	helm upgrade --install canopy ./cluster/canopy/helm -n $(NAMESPACE) --create-namespace --set replicaCount=$(NODES) -f configs/canopy-helm/values.yaml

## test/load: runs the populator load test
.PHONY: test/load
test/load:
	$(call check_vars, CONFIG PROFILE)
	$(MAKE) populator/load

## test/destroy: destroy the load-test-related resources in the canopy namespace
.PHONY: test/destroy
test/destroy:
	$(eval NAMESPACE ?= canopy)
	helm uninstall canopy -n $(NAMESPACE)
	kubectl delete configmap config genesis ids keystore -n $(NAMESPACE)
	kubectl delete svc -l type=chain -n $(NAMESPACE)

## --- manual setup ---
# ==================================================================================== #
# MANUAL SETUP
# ==================================================================================== #

## infra/k3s-server: sets up a K3s server on the local machine
.PHONY: infra/k3s-server
infra/k3s-server:
	./scripts/install-k3s-server.sh

## infra/helm: installs Helm on the cluster
.PHONY: infra/helm
infra/helm:
	@curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

## tls/hetzner: sets up a domain's TLS certificates for the cluster using hetzner DNS
.PHONY: tls/hetzner
tls/hetzner:
	$(call check_vars, DOMAIN HETZNER_API_TOKEN EMAIL)
	DOMAIN=$(DOMAIN) HETZNER_DNS_API_TOKEN=$(HETZNER_API_TOKEN) \
	EMAIL=$(EMAIL) ./cluster/tls/setup.sh

## monitoring/prometheus: sets up Prometheus and Grafana on the cluster
.PHONY: monitoring/prometheus
monitoring/prometheus:
	$(call check_vars, DOMAIN)
	DOMAIN=$(DOMAIN) DISCORD_WEBHOOK_URL=$(DISCORD_WEBHOOK_URL) ./cluster/monitoring/prometheus/setup.sh

## monitoring/loki: sets up Loki on the cluster
.PHONY: monitoring/loki
monitoring/loki:
	./cluster/monitoring/loki/setup.sh

## monitoring/promtail: sets up Promtail on the cluster
.PHONY: monitoring/promtail
monitoring/promtail:
	./cluster/monitoring/promtail/setup.sh

## monitoring: installs the full monitoring stack on the cluster
.PHONY: monitoring
monitoring:
	$(call check_vars, DOMAIN)
	$(MAKE) monitoring/prometheus DOMAIN=$(DOMAIN) DISCORD_WEBHOOK_URL=$(DISCORD_WEBHOOK_URL)
	$(MAKE) monitoring/loki
	$(MAKE) monitoring/promtail

## chaos/mesh: installs or upgrades Chaos Mesh with k3s containerd settings
.PHONY: chaos/mesh
chaos/mesh:
	helm repo add chaos-mesh https://charts.chaos-mesh.org
	helm repo update
	helm upgrade --install chaos-mesh chaos-mesh/chaos-mesh -n chaos-mesh --create-namespace \
		-f ./cluster/chaos-mesh/values.yaml

# ==================================================================================== #
# GO SCRIPTS
# ==================================================================================== #

## go-scripts/build: builds the go scripts for further usage, requires golang to be installed
## usage: make go-scripts/build [GOOS=linux] [GOARCH=amd64]
GOOS ?=
GOARCH ?=
.PHONY: go-scripts/build
go-scripts/build:
	cd ./go-scripts/genesis-generator && GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o ../bin/genesis_apply ./cmd/k8s-applier/main.go
	cd ./go-scripts/genesis-generator && GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o ../bin/genesis_generate ./cmd/genesis/main.go
	cd ./go-scripts/populator && GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o ../bin/populator ./*.go


## genesis/generate: generates the genesis config files based on the given config
.PHONY: genesis/generate
genesis/generate:
	$(call check_vars, CONFIG)
	./go-scripts/bin/genesis_generate --path ./configs/genesis \
	  --output ./configs/genesis/artifacts --config $(CONFIG)

## genesis/apply: applies the config files created by the generator into the cluster
.PHONY: genesis/apply
genesis/apply:
	$(call check_vars, CONFIG)
	$(eval CHAIN_LB ?= false)
	$(eval NAMESPACE ?= canopy)
	./go-scripts/bin/genesis_apply --path ./configs/genesis/artifacts \
		--config $(CONFIG) $(if $(filter true,$(CHAIN_LB)),--chainLB) --namespace $(NAMESPACE)

## populator/load: runs the populator load test
.PHONY: populator/load
populator/load:
	$(call check_vars, CONFIG PROFILE)
	./go-scripts/bin/populator --path ./configs/populator/config.yml \
	--accounts ./configs/genesis/artifacts/$(CONFIG)/ids.json --profile $(PROFILE)

## --- ansible ---
# ==================================================================================== #
# ANSIBLE
# ==================================================================================== #

## ansible/requirements: installs the requirements for the ansible playbook, requires ansible
.PHONY: ansible/requirements
ansible/requirements:
	ansible-galaxy install -r ./ansible/collections/requirements.yml

## ansible/site: creates/adds a new node to a k3s cluster, requires ansible and kubectl
.PHONY: ansible/site
ansible/site:
	ansible-playbook k3s.orchestration.site -e @./ansible/secrets.yml

## ansible/teardown: removes the cluster and all nodes, requires ansible and kubectl
.PHONY: ansible/teardown
ansible/teardown:
	ansible-playbook k3s.orchestration.reset

## ansible/setup: setups the ansible package and runs the playbook to setup the cluster
.PHONY: ansible/setup
ansible/setup:
	$(MAKE) ansible/requirements
	$(MAKE) ansible/site

## ansible/cluster-setup: creates/adds a new node to a k3s cluster, updates it, and sets the tls/monitoring stack
.PHONY: ansible/cluster-setup
ansible/cluster-setup:
	$(MAKE) ansible/setup
	ansible-playbook -i ansible/inventory.yml ansible/playbooks/1-setup.yml
	ansible-playbook -i ansible/inventory.yml ansible/playbooks/2-cilium.yml
	ansible-playbook -i ansible/inventory.yml ansible/playbooks/3-helm.yml
	ansible-playbook -i ansible/inventory.yml ansible/playbooks/4-tls-hetzner.yml \
	  -e @./ansible/secrets.yml
	ansible-playbook -i ansible/inventory.yml ansible/playbooks/5-monitoring.yml \
	  -e @./ansible/secrets.yml

## ansible/dns-rewrite: rewrite external domains so they're locally redirected within the cluster
.PHONY: ansible/dns-rewrite
ansible/dns-rewrite:
	ansible-playbook -i ansible/inventory.yml ansible/playbooks/8-coredns-rewrite.yml \
	  -e @./ansible/secrets.yml

## ansible/ping: ping all nodes in the inventory
.PHONY: ansible/ping
ansible/ping:
	ansible k3s_cluster -m ping

## ansible/copy-image: copy container image to all k3s nodes and import into containerd
.PHONY: ansible/copy-image
ansible/copy-image:
	$(eval IMAGE ?= canopy_image.tar)
	ansible-playbook -i ansible/inventory.yml ansible/playbooks/6-copy-image.yml \
	  -e "image_tar_name=$(IMAGE)"

## --- util ---
# ==================================================================================== #
# UTIL
# ==================================================================================== #

## util/brew-install-requirements: installs kubectl, ansible, cilium and helm with brew
.PHONY: util/brew-install-requirements
util/brew-install-requirements:
	@command -v brew >/dev/null 2>&1 || { echo "Homebrew not found. Install from https://brew.sh and re-run."; exit 1; }
	@brew list kubernetes-cli >/dev/null 2>&1 || brew install kubernetes-cli
	@brew list helm >/dev/null 2>&1 || brew install helm
	@brew list ansible >/dev/null 2>&1 || brew install ansible
	@brew list cilium-cli >/dev/null 2>&1 || brew install cilium-cli
	@echo "kubectl: $$(kubectl version --client 2>/dev/null | grep 'Client Version' | awk '{print $$3}' || echo not installed)"
	@echo "helm:    $$(helm version --short 2>/dev/null || echo not installed)"
	@echo "ansible: $$(ansible --version 2>/dev/null | head -n1 | grep -o '\[core [^]]*\]' || echo not installed)"
	@echo "cilium:  $$(cilium version --client 2>/dev/null | grep 'cilium image (stable):' | awk '{print $$4}' || echo not installed)"

## util/build-deploy: builds and deploys the given script using the tag, requires docker buildx
.PHONY: util/build-deploy
util/build-deploy:
	$(call check_vars, TAG)
	docker buildx build --push --platform linux/amd64,linux/arm64 -t $(TAG) -f ./go-scripts/Dockerfile ./go-scripts/
