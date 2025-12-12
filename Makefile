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

## genesis/apply: applies the config files created by the generator into the cluster
.PHONY: genesis/apply
genesis/apply:
	$(call check_vars, CONFIG)
	./go-scripts/bin/genesis_apply --path ./go-scripts/genesis-generator/artifacts --config $(CONFIG)
