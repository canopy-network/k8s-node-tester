#!/bin/bash

# helper function to get the script directory as this could run from another directory
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# add the prometheus-community helm repository
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

# install prometheus-stack
helm upgrade --install --create-namespace prometheus prometheus-community/kube-prometheus-stack \
  --namespace monitoring -f "${SCRIPT_DIR}/values.yml"

# set up an ingress route to access prometheus and grafana
sed -e "s;{{DOMAIN}};${DOMAIN};g" "${SCRIPT_DIR}/ingress-routes.yml" | kubectl apply -f -

# set up the canopy service monitor to automatically discover and monitor canopy pods
kubectl apply -f "${SCRIPT_DIR}/service-monitor.yml"
