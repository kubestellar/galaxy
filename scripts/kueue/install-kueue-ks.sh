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

# IMPORTANT WHEN RUNNING WITH RANCHER DESKTOP
#
# During the install documented below you may run out of quota for open files/inotify.
# This problem manifests itself by all pods in the cluster being in Pending state.
#
# To prevent the problem, create this file:
#
# touch ~/Library/Application\ Support/rancher-desktop/lima/_config/override.yaml
#
# and add the following content:
#
# provision:
# - mode: system
#   script: |
#    #!/bin/sh
#    cat <<'EOF' > /etc/security/limits.d/rancher-desktop.conf
#    * soft     nofile         82920
#    * hard     nofile         82920
#    EOF
#    sysctl -w vm.max_map_count=262144
#    sysctl -w fs.inotify.max_user_instances=8192
#    sysctl -w fs.inotify.max_user_watches=1048576
#
#
# Restart Rancher Desktop for changes to take effect
#
set -x # echo so that users can understand what is happening
set -e # exit on error

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
source "${SCRIPT_DIR}/../common/setup-shell.sh"
source "${SCRIPT_DIR}/../common/config.sh"

#export KUBESTELLAR_VERSION=0.24.0
export KUBESTELLAR_VERSION=0.25.0-rc.1

#export OCM_STATUS_ADDON_VERSION=0.2.0-rc11
#export OCM_TRANSPORT_PLUGIN=0.1.11

export KUEUE_VERSION=v0.8.1

WORK_DIR=$(mktemp -d -p /tmp)
echo "using ${WORK_DIR} to clone repos"

# Cleanup function to delete the temp directory
function cleanup {
   rm -rf "$WORK_DIR"
   echo "Deleted temp working directory $WORK_DIR"
}

# Register the cleanup function to be called on EXIT signal
trap cleanup EXIT


: ----------------- INSTALLING -----------------

#kubectl --context kubeflex apply -f https://raw.githubusercontent.com/kubeflow/training-operator/855e0960668b34992ba4e1fd5914a08a3362cfb1/manifests/base/crds/kubeflow.org_pytorchjobs.yaml
kubectl --context wds1 create -f ${SCRIPT_DIR}/templates/wec-kueue-ns.yaml
kubectl --context k3d-kubeflex create -f ${SCRIPT_DIR}/templates/wec-bp-kueue.yaml
kubectl --context k3d-kubeflex create -f ${SCRIPT_DIR}/templates/wec-clustermetrics-ns.yaml
kubectl --context wds1 create -f ${SCRIPT_DIR}/templates/wec-clustermetrics-ns.yaml

: Installing kueue controller on control cluster
kubectl --context k3d-kubeflex apply --server-side -f https://github.com/kubernetes-sigs/kueue/releases/download/$KUEUE_VERSION/manifests.yaml

: waiting for kueue controller to come up
wait-for-cmd '(($(wrap-cmd kubectl --context k3d-kubeflex get deployments -n kueue-system -o jsonpath='{.status.readyReplicas}' kueue-controller-manager 2>/dev/null || echo 0) >= 1))'

sleep 10

kubectl --context k3d-kubeflex create -f ${SCRIPT_DIR}/templates/admissioncheck-ks.yaml
kubectl --context k3d-kubeflex create -f ${SCRIPT_DIR}/templates/user-queue-ks.yaml
kubectl --context k3d-kubeflex create -f ${SCRIPT_DIR}/templates/h100-resource-flavor.yaml
kubectl --context k3d-kubeflex create -f ${SCRIPT_DIR}/templates/spot-resource-flavor.yaml
kubectl --context k3d-kubeflex create -f ${SCRIPT_DIR}/templates/a100-resource-flavor.yaml
kubectl --context k3d-kubeflex create -f ${SCRIPT_DIR}/templates/zero-cluster-gpus-queue-ks.yaml

kubectl config use-context k3d-kubeflex

#: Deploy clustermetrics to  each Cluster
#: install cluster-metrics controller on all clusters

cd ${SCRIPT_DIR}/../../clustermetrics

#make ko-local-build

kubectl --context k3d-kubeflex apply -f config/crd/bases
kubectl --context wds1 apply -f config/crd/bases

: Deploy manifests to WDS1 and will trigger pull from each wec
#kubectl --context wds1 create -f ${SCRIPT_DIR}/templates/wec-manifests.yaml

kubectl --context wds1 create -f ${SCRIPT_DIR}/templates/wec-kueue-ks-ns.yaml
kubectl --context wds1 create -f ${SCRIPT_DIR}/templates/wec-rbac-kueue-ks.yaml
kubectl --context wds1 create -f ${SCRIPT_DIR}/templates/wec-kueue-clusterqueue-crd.yaml
kubectl --context wds1 create -f ${SCRIPT_DIR}/templates/wec-cluster-queue.yaml
kubectl --context wds1 create -f ${SCRIPT_DIR}/templates/wec-bp-kueue-ks.yaml
kubectl --context wds1 create -f ${SCRIPT_DIR}/templates/wec-bp-kueue.yaml
kubectl --context wds1 create -f ${SCRIPT_DIR}/templates/wec-bp-clustermetrics.yaml
kubectl --context wds1 create -f ${SCRIPT_DIR}/templates/wec-rbac-clustermetrics.yaml

#kubectl --context wds1 create -f ${SCRIPT_DIR}/templates/kueue-manifests.yaml

#contexts=(cluster1 cluster2);
#for context in "${contexts[@]}"; do
#    : Installing kueue controller on the WEC
#    kubectl --context ${context} apply --server-side -f https://github.com/kubernetes-sigs/kueue/releases/download/$KUEUE_VERSION/manifests.yaml

 #   : waiting for kueue controller to come up on the WEC
 #   wait-for-cmd '(($(wrap-cmd kubectl --context ${context} get deployments -n kueue-system -o jsonpath='{.status.readyReplicas}' kueue-controller-manager 2>/dev/null || echo 0) >= 1))'


# GOOD   kubectl --context ${context} apply -f config/crd/bases
# GGOD    kubectl --context ${context} apply -f ${SCRIPT_DIR}/../common/templates/cluster-metrics-rbac.yaml
#    kubectl --context ${context} apply -f ${SCRIPT_DIR}/../common/templates/cluster-metrics-${context}.yaml
    #luster=${context}
    # GOOD CONTEXT=${context} CLUSTER=${cluster} HELM_OPTS="--set clusterName=${cluster}" make k3d-install-local-chart
    #kubectl --context ${context} apply -f https://raw.githubusercontent.com/project-codeflare/appwrapper/main/config/crd/bases/workload.codeflare.dev_appwrappers.yaml
    #kubectl --context ${context} apply -k "github.com/kubeflow/training-operator/manifests/overlays/standalone?ref=v1.7.0"
# GOOD    kubectl --context k3d-kubeflex apply -f ${SCRIPT_DIR}/../common/templates/cluster-metrics-${cluster}.yaml
  #  kubectl --context ${context} apply -f https://raw.githubusercontent.com/kubeflow/training-operator/855e0960668b34992ba4e1fd5914a08a3362cfb1/manifests/base/crds/kubeflow.org_pytorchjobs.yaml
#done

kubectl --context k3d-kubeflex apply -f https://raw.githubusercontent.com/project-codeflare/appwrapper/main/config/crd/bases/workload.codeflare.dev_appwrappers.yaml

# GOOD kubectl --context k3d-kubeflex apply -f  ${SCRIPT_DIR}/templates/status-collector.yaml

    kubectl --context wds1 apply -f https://raw.githubusercontent.com/project-codeflare/appwrapper/main/config/crd/bases/workload.codeflare.dev_appwrappers.yaml
#    kubectl --context wds1 apply -k "github.com/kubeflow/training-operator/manifests/overlays/standalone?ref=v1.7.0"
#    kubectl --context wds1 apply -f https://raw.githubusercontent.com/kubeflow/training-operator/855e0960668b34992ba4e1fd5914a08a3362cfb1/manifests/base/crds/kubeflow.org_pytorchjobs.yaml


cd ${SCRIPT_DIR}

ATTEMPTS=100
INTERVAL=5

for i in $(seq 1 $ATTEMPTS); do
    if kubectl get crd bindingpolicies.control.kubestellar.io >/dev/null 2>&1; then
	echo "bindingpolicy crd installed"
	break
    else
	sleep $INTERVAL
    fi
done


kubectl config use-context k3d-kubeflex

#kubectl --context kubeflex create -f ${SCRIPT_DIR}/templates/transform-pytorch-job.yaml

for cluster in "${clusters[@]}"; do
#      kubectl --context k3d-kubeflex apply -f ${SCRIPT_DIR}/templates/binding-policy-${cluster}.yaml
#      kubectl --context k3d-kubeflex apply -f ${SCRIPT_DIR}/templates/wl-binding-policy-${cluster}.yaml
#kubectl --context ${cluster} apply -f - <<EOF
kubectl --context wds1 apply -f - <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: appwrappers-access
  labels:
    app.kubernetes.io/name: kueue-ks
rules:
- apiGroups: ["workload.codeflare.dev"]
  resources: ["appwrappers"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["workloads.kueue.x-k8s.io"]
  resources: ["workload"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["galaxy.kubestellar.io"]
  resources: ["clustermetrics"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["kubeflow.org"]
  resources: ["pytorchjobs"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]

---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: klusterlet-appwrappers-access
  labels:
    app.kubernetes.io/name: kueue-ks
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: appwrappers-access
subjects:
- kind: ServiceAccount
  name: klusterlet-work-sa
  namespace: open-cluster-management-agent
EOF
done

contexts=(cluster1 cluster2);
for context in "${contexts[@]}"; do
  : Wait for Kueue manifests to propagate from wds1 to each wec and for kueue controller to start
  wait-for-cmd '(($(wrap-cmd kubectl --context ${context} get deployments -n kueue-system -o jsonpath='{.status.readyReplicas}' kueue-controller-manager 2>/dev/null || echo 0) >= 1))'
done


: install kueue-ks

helm --kube-context k3d-kubeflex upgrade --install kueue-ks \
      ${SCRIPT_DIR}/../../charts/kueue-ks \
      --create-namespace --namespace kueue-ks-system 
    

