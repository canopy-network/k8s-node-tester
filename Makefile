.PHONY: install-k3s-server
install-k3s-server:
	./scripts/install-k3s-server.sh

.PHONY: install-helm
install-helm:
	@curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
