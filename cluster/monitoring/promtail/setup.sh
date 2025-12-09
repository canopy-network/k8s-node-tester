#!/bin/bash

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

helm upgrade --install promtail grafana/promtail --namespace monitoring -f "${SCRIPT_DIR}/values.yml"
