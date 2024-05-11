#!/bin/bash

set -x # echo so that users can understand what is happening
set -e # exit on error

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
source "${SCRIPT_DIR}/../common/setup-shell.sh"
source "${SCRIPT_DIR}/../common/config.sh"

: install argo workflows on all clusters

# pre-load images that experienced docker registry rate limit when loading multiple times in kind
images=(docker/whalesay:latest alpine:3.7 python:alpine3.6 minio/minio:RELEASE.2022-11-17T23-20-09Z)
for image in "${images[@]}"; do
  docker pull ${image}
  for cluster in "${clusters[@]}"; do
     kind load docker-image --name ${cluster} ${image}
  done
done

all_clusters=("${clusters[@]}")
all_clusters+=("wds0")
for cluster in "${all_clusters[@]}"; do
  kubectl --context ${cluster} create namespace argo
  kubectl --context ${cluster} apply -n argo -f https://github.com/argoproj/argo-workflows/releases/download/v3.5.5/quick-start-minimal.yaml
done

: wait until argo server and workflow controller is up on all clusters
for cluster in "${all_clusters[@]}"; do
  wait-for-cmd '(($(wrap-cmd kubectl --context ${cluster} get deployments -n argo -o jsonpath='{.status.readyReplicas}' argo-server 2>/dev/null || echo 0) >= 1))'
  wait-for-cmd '(($(wrap-cmd kubectl --context ${cluster} get deployments -n argo -o jsonpath='{.status.readyReplicas}' workflow-controller 2>/dev/null || echo 0) >= 1))'
done

: install service and ingress for argo on wds0

kubectl --context wds0 apply -f ${SCRIPT_DIR}/templates/ingress.yaml

: install nodeport service on wds0 for minio

kubectl --context wds0 apply -f ${SCRIPT_DIR}/templates/minio-service.yaml

: update configmap for wecs to use a common s3 from kubeflex-control-plane

nodePort=$(kubectl --context wds0 -n argo get service minio-nodeport -o jsonpath='{.spec.ports[0].nodePort}')
for cluster in "${clusters[@]}"; do
  kubectl --context ${cluster} -n argo get cm artifact-repositories -o yaml > /tmp/artifact-repositories.yaml
  sed -i.bak "s/minio:9000/kubeflex-control-plane:${nodePort}/g" /tmp/artifact-repositories.yaml
  kubectl --context ${cluster} -n argo apply -f /tmp/artifact-repositories.yaml
fi

: give permissions to klusterlets to manage workflows

for cluster in "${clusters[@]}"; do
  kubectl --context ${cluster} apply -f ${SCRIPT_DIR}/templates/argo-rbac.yaml
done

: install mutating admission webhook for workflows

make ko-local-build && make install-local-chart

: wait for admission webhook to be up

wait-for-cmd '(($(wrap-cmd kubectl --context wds0 get deployments -n ksi-system -o jsonpath='{.status.readyReplicas}' ksi-ks-integration 2>/dev/null || echo 0) >= 1))'

: test with one workflow created in wds0 

kubectl --context wds0 create -f  ${SCRIPT_DIR}/samples/argo-wf1.yaml

: check admission set suspend to true

wait-for-cmd 'kubectl get workflows -n argo -o yaml | grep "suspend: true"'
: "SUCCESS: mutating admission webhook suspended the workflow"

: create binding policy

kubectl --context wds0 apply -f  ${SCRIPT_DIR}/samples/wf-binding-policy.yaml

: check workflow is propagated downstream

wait-for-cmd 'kubectl --context cluster1 -n argo get workflows | grep "hello-world-"'
: "SUCCESS: confirmed workflow deployed on cluster1"

: check workflow is completed on cluster1

wait-for-cmd 'kubectl --context cluster1 get workflow -n argo --no-headers | grep "hello-world-" | grep Succeeded'
: "SUCCESS: confirmed workflow completed on cluster1"

: check workflow is completed on wds0

wait-for-cmd 'kubectl --context wds0 get workflow -n argo --no-headers | grep "hello-world-" | grep Succeeded'
: "SUCCESS: confirmed workflow status updated on wdso"

: Deploy a second workflow

kubectl --context wds0 create -f  ${SCRIPT_DIR}/samples/argo-wf2.yaml

: check second workflow is completed on wds0

wait-for-cmd 'kubectl --context wds0 get workflow -n argo --no-headers | grep "hello-world2-" | grep Succeeded'
: "SUCCESS: confirmed second workflow status updated on wdso"

echo "you can access the argo console at https://argo.localtest.me:9443"
echo "you can access the minio console at http://minio.localtest.me:9080"