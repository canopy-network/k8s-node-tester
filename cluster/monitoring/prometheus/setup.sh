#!/bin/bash

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

helm upgrade --install --create-namespace prometheus prometheus-community/kube-prometheus-stack \
  --namespace monitoring -f "${SCRIPT_DIR}/values.yml"

sed -e "s;{{DOMAIN}};${DOMAIN};g" "${SCRIPT_DIR}/ingress-routes.yml" | kubectl apply -f -
