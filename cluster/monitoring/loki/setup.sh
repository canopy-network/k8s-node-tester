#!/bin/bash

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

helm repo add grafana https://grafana.github.io/helm-charts
helm repo update

helm upgrade --install loki grafana/loki --namespace monitoring \
  -f "${SCRIPT_DIR}/values.yml" --version 3.6.3
