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
source "${SCRIPT_DIR}/../common/setup-shell.sh"
source "${SCRIPT_DIR}/../common/config.sh"

install_webhook=$1

: install argo workflows on all clusters

# pre-load images that experienced docker registry rate limit when loading multiple times in kind
images=(alpine:3.7 python:alpine3.6 minio/minio:RELEASE.2022-11-17T23-20-09Z)
for image in "${images[@]}"; do
  docker pull ${image}
  for cluster in "${clusters[@]}"; do
     kind load docker-image --name ${cluster} ${image}
  done
done

all_clusters=("${clusters[@]}")
all_clusters+=("kind-kubeflex")
for cluster in "${all_clusters[@]}"; do
  kubectl --context ${cluster} create namespace argo
  kubectl --context ${cluster} apply -n argo -f https://github.com/argoproj/argo-workflows/releases/download/v3.5.6/quick-start-minimal.yaml
done

: wait until argo server and workflow controller is up on all clusters
for cluster in "${all_clusters[@]}"; do
  wait-for-cmd '(($(wrap-cmd kubectl --context ${cluster} get deployments -n argo -o jsonpath='{.status.readyReplicas}' argo-server 2>/dev/null || echo 0) >= 1))'
  wait-for-cmd '(($(wrap-cmd kubectl --context ${cluster} get deployments -n argo -o jsonpath='{.status.readyReplicas}' workflow-controller 2>/dev/null || echo 0) >= 1))'
done

: install service and ingress for argo on wds0

kubectl --context kind-kubeflex apply -f ${SCRIPT_DIR}/templates/ingress.yaml

: install nodeport service on wds0 for minio

kubectl --context kind-kubeflex apply -f ${SCRIPT_DIR}/templates/minio-service.yaml

: update configmap for wecs to use a common s3 from kubeflex-control-plane

nodePort=$(kubectl --context kind-kubeflex -n argo get service minio-nodeport -o jsonpath='{.spec.ports[0].nodePort}')
for cluster in "${clusters[@]}"; do
  kubectl --context ${cluster} -n argo get cm artifact-repositories -o yaml > /tmp/artifact-repositories.yaml
  sed -i.bak "s/minio:9000/kubeflex-control-plane:${nodePort}/g" /tmp/artifact-repositories.yaml
  kubectl --context ${cluster} -n argo apply -f /tmp/artifact-repositories.yaml
done

: give permissions to klusterlets to manage workflows

for cluster in "${clusters[@]}"; do
  kubectl --context ${cluster} apply -f ${SCRIPT_DIR}/templates/argo-rbac.yaml
done

: create custom transform for argo workflows

kubectl --context kind-kubeflex apply -f  ${SCRIPT_DIR}/templates/workflow-ct.yaml

: create binding policies for argo workflows

for cluster in "${clusters[@]}"; do
  kubectl --context kind-kubeflex apply -f ${SCRIPT_DIR}/samples/wf-binding-policy-${cluster}.yaml
done

# install webhook only if flag --webhook is present
if [[ "${install_webhook}" == "--webhook" ]]; then
  : install mutating admission webhook for workflows on kind-kubeflex

  cd ${SCRIPT_DIR}/../../suspend-webhook
  kubectl config use-context kind-kubeflex
  make webhook-local-build && make install-webhook-local-chart

  : wait for admission webhook to be up

  wait-for-cmd '(($(wrap-cmd kubectl --context kind-kubeflex get deployments -n ksi-system -o jsonpath='{.status.readyReplicas}' suspend-webhook 2>/dev/null || echo 0) >= 1))'
fi

echo "you can access the argo console at https://argo.localtest.me:9443"
echo "you can access the minio console at http://minio.localtest.me:9080"
