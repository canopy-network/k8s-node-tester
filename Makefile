define check_vars
	$(foreach var,$1,$(if $(value $(var)),,$(error ERROR: $(var) is not set)))
endef

.PHONY: infra/k3s-server-setup
k3s-server-setup:
	./scripts/install-k3s-server.sh

.PHONY: infra/helm
infra/helm:
	@curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

.PHONY: tls/hetzner
tls/hetzner:
	$(call check_vars, DOMAIN HETZNER_API_TOKEN EMAIL)
	DOMAIN=$(DOMAIN) HETZNER_DNS_API_TOKEN=$(HETZNER_API_TOKEN) \
	EMAIL=$(EMAIL) ./cluster/tls/setup.sh

.PHONY: monitoring/prometheus
monitoring/prometheus:
	$(call check_vars, DOMAIN)
	DOMAIN=$(DOMAIN) ./cluster/monitoring/prometheus/setup.sh

.PHONY: monitoring/loki
monitoring/loki:
	./cluster/monitoring/loki/setup.sh

.PHONY: monitoring/promtail
monitoring/promtail:
	./cluster/monitoring/promtail/setup.sh
