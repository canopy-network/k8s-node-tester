#!/bin/bash

# install the cert-manager
helm repo update
helm upgrade --install \
  cert-manager oci://quay.io/jetstack/charts/cert-manager \
  --version v1.19.1 \
  --namespace cert-manager \
  --create-namespace \
  --set crds.enabled=true

# install the hetzner webhook
helm repo add hcloud https://charts.hetzner.cloud
helm repo update hcloud
helm install cert-manager-webhook-hetzner hcloud/cert-manager-webhook-hetzner -n cert-manager

# Wait for cert-manager and cert-manager-webhook-hetzner to be ready
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=cert-manager -n cert-manager --timeout=120s
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=cert-manager-webhook-hetzner -n cert-manager --timeout=120s

# apply the certificate using variable substitution
sed -e "s;{{HETZNER_API_TOKEN}};${HETZNER_API_TOKEN};g" ./cluster/tls/hetzner-tls.yml | \
sed -e "s;{{EMAIL}};${EMAIL};g" | \
sed -e "s;{{DOMAIN}};${DOMAIN};g" | kubectl apply -f -

echo "done ✅. Bear in mind that the certificate validation may take several minutes, after that,"
echo "restart the pods behind traefik's reverse proxy in order for this to take effect."
echo "You can check the status of the certificate with the following command:"
echo "kubectl get certificate -n cert-manager"
