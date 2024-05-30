#!/bin/bash

# Copyright 2024 The KubeStellar Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -x # echo so that users can understand what is happening
set -e # exit on error

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
source "${SCRIPT_DIR}/setup-shell.sh"
source "${SCRIPT_DIR}/config.sh"

: --------------------------------------------------------------

: install loki on hub cluster

helm repo add grafana https://grafana.github.io/helm-charts
helm repo update

helm --kube-context kind-kubeflex upgrade --install loki grafana/loki-stack \
  --create-namespace \
  --namespace loki \
  --set promtail.enabled=false \
  --set grafana.enabled=false \
  --set loki.persistence.enabled=true \
  --set loki.persistence.size=10Gi \
  --set loki.config.limits_config.max_concurrent_tail_requests=30

: create nodeport for loki service

kubectl --context kind-kubeflex apply -f ${SCRIPT_DIR}/templates/loki-nodeport-service.yaml

: get nodeport value

lokiNPort=$(kubectl --context kind-kubeflex -n loki get service loki-nodeport -o jsonpath='{.spec.ports[0].nodePort}')

: install promtail on WECs

for cluster in "${clusters[@]}"; do
    helm --kube-context ${cluster} upgrade --install promtail grafana/promtail --set "config.clients[0].url=http://kubeflex-control-plane:${lokiNPort}/loki/api/v1/push" 
done


