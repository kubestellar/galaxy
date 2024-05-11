#!/bin/bash

set -x # echo so that users can understand what is happening
set -e # exit on error

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
source "${SCRIPT_DIR}/../common/setup-shell.sh"
source "${SCRIPT_DIR}/../common/config.sh"

export PIPELINE_VERSION=2.2.0

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

contexts=(wds0);
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

kubectl --context wds0 -n kubeflow get secret mlpipeline-minio-artifact -o yaml > ${WORK_DIR}/mlpipeline-minio-artifact.yaml
sed -i.bak -e '/creationTimestamp:/d' \
           -e '/resourceVersion:/d' \
           -e '/uid:/d' \
           -e '/selfLink:/d' \
           -e '/kubectl.kubernetes.io\/last-applied-configuration:/{N;d;}' \
           ${WORK_DIR}/mlpipeline-minio-artifact.yaml

: install kfp with kustomization on execution clusters

minioNPort=$(kubectl --context wds0 -n kubeflow get service minio-nodeport -o jsonpath='{.spec.ports[0].nodePort}')
mysqlNPort=$(kubectl --context wds0 -n kubeflow get service mysql-nodeport -o jsonpath='{.spec.ports[0].nodePort}')
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

: install mutating admission webhook for workflows on wds0

cd ${SCRIPT_DIR}/../../suspend-webhook
kubectl config use-context wds0
make webhook-local-build && make install-webhook-local-chart

: wait for admission webhook to be up

wait-for-cmd '(($(wrap-cmd kubectl --context wds0 get deployments -n ksi-system -o jsonpath='{.status.readyReplicas}' suspend-webhook 2>/dev/null || echo 0) >= 1))'

: install shadow pods controller

cd ${SCRIPT_DIR}/../../shadow-pods
kubectl config use-context wds0
make loki-logger-local-build && make shadow-local-build && make install-shadow-local-chart

: create binding policies for argo workflows

for cluster in "${clusters[@]}"; do
  kubectl --context wds0 apply -f  ${SCRIPT_DIR}/templates/wf-binding-policy-${cluster}.yaml
done

: create custom transform for argo workflows

kubectl --context wds0 apply -f  ${SCRIPT_DIR}/templates/workflow-ct.yaml

: wait until all KFP deployments for kubeflow are up in all clusters, this may take tens of minutes

set +x
contexts=("${clusters[@]}")
contexts+=("wds0")
for context in "${contexts[@]}"; do
  while true; do
      if [[ $(check_pods_ready ${context}) == true ]]; then
          echo "All deployments for ${context} are in ready state."
          break
      else
          echo "Not all deployments in ${context} are ready yet. Waiting..."
          sleep 5
      fi
  done
done
set -x
