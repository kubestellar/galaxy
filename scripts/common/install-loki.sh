#!/bin/bash

set -x # echo so that users can understand what is happening
set -e # exit on error

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
source "${SCRIPT_DIR}/setup-shell.sh"
source "${SCRIPT_DIR}/config.sh"

: --------------------------------------------------------------

: install loki on hub cluster

helm repo add grafana https://grafana.github.io/helm-charts
helm repo update

helm --kube-context wds0 upgrade --install loki grafana/loki-stack \
  --create-namespace \
  --namespace loki \
  --set promtail.enabled=false \
  --set grafana.enabled=false \
  --set loki.persistence.enabled=true \
  --set loki.persistence.size=10Gi \
  --set loki.config.limits_config.max_concurrent_tail_requests=30

: create nodeport for loki service

kubectl --context wds0 apply -f ${SCRIPT_DIR}/templates/loki-nodeport-service.yaml

: get nodeport value

lokiNPort=$(kubectl --context wds0 -n loki get service loki-nodeport -o jsonpath='{.spec.ports[0].nodePort}')

: install promtail on WECs

for cluster in "${clusters[@]}"; do
    helm --kube-context ${cluster} upgrade --install promtail grafana/promtail --set "config.clients[0].url=http://kubeflex-control-plane:${lokiNPort}/loki/api/v1/push" 
done


