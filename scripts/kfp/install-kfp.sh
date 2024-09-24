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

HOSTING_CLUSTER_NODE=kubeflex-control-plane
WORK_DIR=$(mktemp -d -p /tmp)
echo "using ${WORK_DIR} to clone repos"

# Cleanup function to delete the temp directory
function cleanup {
    rm -rf "$WORK_DIR"
    echo "Deleted temp working directory $WORK_DIR"
}

# Register the cleanup function to be called on EXIT signal
trap cleanup EXIT

check_pods_ready() {
    context=$1
    local allReady=true
    deployments=$(kubectl --context ${context} get deployments -n kubeflow -o jsonpath='{.items[*].metadata.name}')
    for deployment in $deployments; do
        readyReplicas=$(kubectl --context ${context} get deployment $deployment -n kubeflow -o jsonpath='{.status.readyReplicas}')
        if [[ "$readyReplicas" -lt 1 ]]; then
            allReady=false
            break
        fi
    done
    echo $allReady
}

updateArgoConfig() {
CONTEXT=$1
NAMESPACE=kubeflow
CONFIGMAP_NAME="workflow-controller-configmap"

new_workflow_defaults=$(cat <<EOF
workflowDefaults: |
  spec:
    ttlStrategy:
      secondsAfterCompletion: 604800
      secondsAfterSuccess: 604800
      secondsAfterFailure: 3600
EOF
)

json_workflow_defaults=$(echo "$new_workflow_defaults" | yq eval -o=json)

kubectl --context ${CONTEXT} -n ${NAMESPACE} patch configmap "$CONFIGMAP_NAME" --type merge -p "{\"data\": $json_workflow_defaults}"
}


##################################################################################################

: pre-load images that experienced docker registry rate limit when loading multiple times in kind

images=(alpine:3.7 python:alpine3.6 minio/minio:RELEASE.2022-11-17T23-20-09Z python:3.7)
all_clusters=("${clusters[@]}")
all_clusters+=("kubeflex")
for image in "${images[@]}"; do
  docker pull ${image}
  for cluster in "${all_clusters[@]}"; do
     kind load docker-image --name ${cluster} ${image}
  done
done

: install kfp on kubeflex and add services and ingresses

contexts=(kind-kubeflex);
for context in "${contexts[@]}"; do
  kubectl --context ${context} apply -k "github.com/kubeflow/pipelines/manifests/kustomize/cluster-scoped-resources?ref=$PIPELINE_VERSION"
  kubectl --context ${context} wait --for condition=established --timeout=60s crd/applications.app.k8s.io
  kubectl --context ${context} apply -k "github.com/kubeflow/pipelines/manifests/kustomize/env/platform-agnostic-emissary?ref=$PIPELINE_VERSION"
  kubectl --context ${context} -n kubeflow apply -f ${SCRIPT_DIR}/templates/service.yaml
  kubectl --context ${context} -n kubeflow apply -f ${SCRIPT_DIR}/templates/ingress.yaml
  kubectl --context ${context} -n kubeflow set env deployment ml-pipeline-ui ARGO_ARCHIVE_LOGS="true"
  # replace default UI image "gcr.io/ml-pipeline/frontend:2.2.0" with patched image from https://github.com/pdettori/pipelines/tree/s3-pod-logs-fix
  kubectl --context ${context} -n kubeflow set image deployment/ml-pipeline-ui ml-pipeline-ui=quay.io/pdettori/kfp-frontend:2.2.0-fix
  updateArgoConfig ${context}
done

: prepare minio secret for WECs

kubectl --context kind-kubeflex -n kubeflow get secret mlpipeline-minio-artifact -o yaml > ${WORK_DIR}/mlpipeline-minio-artifact.yaml
sed -i.bak -e '/creationTimestamp:/d' \
           -e '/resourceVersion:/d' \
           -e '/uid:/d' \
           -e '/selfLink:/d' \
           -e '/kubectl.kubernetes.io\/last-applied-configuration:/{N;d;}' \
           ${WORK_DIR}/mlpipeline-minio-artifact.yaml

: install kfp with kustomization on execution clusters

minioNPort=$(kubectl --context kind-kubeflex -n kubeflow get service minio-nodeport -o jsonpath='{.spec.ports[0].nodePort}')
mysqlNPort=$(kubectl --context kind-kubeflex -n kubeflow get service mysql-nodeport -o jsonpath='{.spec.ports[0].nodePort}')
echo "minioNPort=${minioNPort} mysqlNPort=${mysqlNPort}"
sed "s/NODE_PORT/${minioNPort}/g" ${SCRIPT_DIR}/templates/minio-proxy-template.yaml > ${WORK_DIR}/minio-proxy.yaml

cat > ${SCRIPT_DIR}/kustomize/base/patch-pipeline-install-config.yaml <<EOL
apiVersion: v1
kind: ConfigMap
metadata:
  name: pipeline-install-config
data:
  dbHost: ${HOSTING_CLUSTER_NODE}
  dbPort: "${mysqlNPort}"
  mysqlHost: ${HOSTING_CLUSTER_NODE}
  mysqlPort: "${mysqlNPort}"
EOL

# use kustomization to avoid installing minio and mysql
for cluster in "${clusters[@]}"; do
  kubectl --context ${cluster} apply -k "github.com/kubeflow/pipelines/manifests/kustomize/cluster-scoped-resources?ref=$PIPELINE_VERSION"
  kubectl --context ${cluster} wait --for condition=established --timeout=60s crd/applications.app.k8s.io
  kubectl --context ${cluster} apply -k ${SCRIPT_DIR}/kustomize/base
  updateArgoConfig ${cluster}

  : apply minio secret
  kubectl --context ${cluster} apply -f ${WORK_DIR}/mlpipeline-minio-artifact.yaml

  : update ui image
  # replace default UI image "gcr.io/ml-pipeline/frontend:2.2.0" with patched image from https://github.com/pdettori/pipelines/tree/s3-pod-logs-fix
  kubectl --context ${cluster} -n kubeflow set image deployment/ml-pipeline-ui ml-pipeline-ui=quay.io/pdettori/kfp-frontend:2.2.0-fix

  : setup reverse proxy for minio so that minio-service proxies to the common s3 from kubeflex-control-plane
  kubectl --context ${cluster} apply -f ${WORK_DIR}/minio-proxy.yaml

  : give permissions to klusterlets to manage workflows
  kubectl --context ${cluster} apply -f ${SCRIPT_DIR}/templates/argo-rbac.yaml
done
: cleanup dynamic patch
rm ${SCRIPT_DIR}/kustomize/base/patch-pipeline-install-config.yaml

: install mutating admission webhook for workflows on kind-kubeflex

helm --kube-context ${core} upgrade --install suspend-webhook \
    oci://ghcr.io/kubestellar/galaxy/suspend-webhook-chart \
    --version ${SUSPEND_WEBHOOK_VERSION} \
    --create-namespace --namespace ksi-system \
    --set image.repository=ghcr.io/kubestellar/galaxy/suspend-webhook \
    --set image.tag=${SUSPEND_WEBHOOK_VERSION}

: wait for admission webhook to be up

wait-for-deployment ${core} ksi-system suspend-webhook-suspend-webhook-chart

: install shadow pods controller

helm --kube-context ${core} upgrade --install shadow-pods \
    oci://ghcr.io/kubestellar/galaxy/shadow-pods-chart \
    --version ${SHADOW_PODS_VERSION} \
    --create-namespace --namespace ksi-system \
    --set image.repository=ghcr.io/kubestellar/galaxy/shadow-pods \
    --set image.tag=${SHADOW_PODS_VERSION} \
    --set lokiLoggerImage.repository=ghcr.io/kubestellar/galaxy/loki-logger \
    --set lokiLoggerImage.tag=${SHADOW_PODS_VERSION} \
    --set lokiInstallType=dev

: iwait for shadow pods controller to be up and running

wait-for-deployment ${core} ksi-system shadow-pods-shadow-pods-chart 

: create binding policies for argo workflows

for cluster in "${clusters[@]}"; do
  kubectl --context  ${core}  apply -f  ${SCRIPT_DIR}/templates/wf-binding-policy-${cluster}.yaml
done

: create custom transform for argo workflows

kubectl --context  ${core} apply -f  ${SCRIPT_DIR}/templates/workflow-ct.yaml

: wait until all KFP deployments for kubeflow are up in all clusters, this may take tens of minutes

set +x
contexts=("${clusters[@]}")
contexts+=("${core}")
for context in "${contexts[@]}"; do
   echo "checking all pods ready in context ${context} kubeflow. Please wait...." 
   wait-for-all-deployments-in-namespace ${context} kubeflow
done
set -x
