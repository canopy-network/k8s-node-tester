define check_vars
	$(foreach var,$1,$(if $(value $(var)),,$(error ERROR: $(var) is not set)))
endef

## help: print each command's help message
.PHONY: help
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' |  sed -e 's/^/ /'

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
	DOMAIN=$(DOMAIN) ./cluster/monitoring/prometheus/setup.sh

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
	$(MAKE) monitoring/prometheus DOMAIN=$(DOMAIN)
	$(MAKE) monitoring/loki
	$(MAKE) monitoring/promtail

## go-scripts/build: builds the go scripts for further usage, requires golang to be installed
.PHONY: go-scripts/build
go-scripts/build:
	cd ./go-scripts/genesis-generator && go build -o ../bin/genesis_apply ./cmd/k8s-applier/main.go

## genesis/apply: applies the config files created by the generator into the cluster
.PHONY: genesis/apply
genesis/apply:
	$(call check_vars, CONFIG)
	./go-scripts/bin/genesis_apply --path ./go-scripts/genesis-generator/artifacts --config $(CONFIG)

## ansible/requirements: installs the requirements for the ansible playbook, requires ansible
.PHONY: ansible/requirements
ansible/requirements:
	ansible-galaxy install -r ./ansible/collections/requirements.yml

## ansible/site: creates/adds a new node to a k3s cluster, requires ansible and kubectl
.PHONY: ansible/site
ansible/site:
	ansible-playbook k3s.orchestration.site -e @./ansible/secrets.yml

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
	ansible-playbook -i ansible/inventory.yml ansible/playbooks/2-helm.yml
	ansible-playbook -i ansible/inventory.yml ansible/playbooks/3-tls-hetzner.yml
	ansible-playbook -i ansible/inventory.yml ansible/playbooks/4-monitoring.yml \
	-e @./ansible/secrets.yml

## ansible/teardown: removes the cluster and all nodes, requires ansible and kubectl
.PHONY: ansible/teardown
ansible/teardown:
	ansible-playbook k3s.orchestration.reset
