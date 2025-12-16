#!/bin/bash

# helper function to get the script directory as this could run from another directory
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# add the prometheus-community helm repository
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

# create the monitoring namespace if it doesn't exist
kubectl create namespace monitoring || true

# import Grafana dashboards (ConfigMaps labeled grafana_dashboard=1) from the dashboards directory
kubectl apply -f "${SCRIPT_DIR}/dashboards"

# install prometheus-stack
helm upgrade --install prometheus prometheus-community/kube-prometheus-stack \
  --namespace monitoring -f "${SCRIPT_DIR}/values.yml" \
  --set 'grafana.alerting.contactpoints\.yml.secret.contactPoints[0].receivers[0].settings.url'="${DISCORD_WEBHOOK_URL}" \
  --version 80.4.1

# set up an ingress route to access prometheus and grafana
sed -e "s;{{ DOMAIN }};${DOMAIN};g" "${SCRIPT_DIR}/ingress-routes.yml" | kubectl apply -f -

# create the canopy namespace if it doesn't exist
kubectl create namespace canopy || true

# set up the canopy service monitor to automatically discover and monitor canopy pods
kubectl apply -f "${SCRIPT_DIR}/service-monitor.yml"
