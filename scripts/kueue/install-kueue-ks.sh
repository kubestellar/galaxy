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

export KUBESTELLAR_VERSION=0.22.0
export OCM_STATUS_ADDON_VERSION=0.2.0-rc8
export OCM_TRANSPORT_PLUGIN=0.1.7
export KUEUE_VERSION=v0.6.2

WORK_DIR=$(mktemp -d -p /tmp)
echo "using ${WORK_DIR} to clone repos"

# Cleanup function to delete the temp directory
function cleanup {
   rm -rf "$WORK_DIR"
   echo "Deleted temp working directory $WORK_DIR"
}

# Register the cleanup function to be called on EXIT signal
trap cleanup EXIT

: --------------------------------------------------------------

: clean up all

kubectl config use-context kubeflex

if ! output=$(kubectl get  deployment kueue-controller-manager -n kueue-system --no-headers 2>&1); then
    printf "kueue deployment does not exists, not need to delete it\n" >&2
else
    printf "Deleting kueue \n" >&2
    kubectl delete --ignore-not-found -f https://github.com/kubernetes-sigs/kueue/releases/download/${KUEUE_VERSION}/manifests.yaml
    echo "Deleted!"
fi

: Deleting bindingpolicies, appwrappers, jobs, clustermetrics

if ! output=$(kubectl --context kubeflex delete appwrappers --all  2>&1); then
  echo "" >&2
fi

kubectl --context kubeflex delete jobs --all --ignore-not-found
kubectl --context kubeflex delete bindingpolicies --all --ignore-not-found

: Deleting kueue-ks controllers
if ! output=$(  helm delete kueue-ks -n kueue-ks-system --ignore-not-found  2>&1); then
   echo "" >&2
fi 
kubectl delete serviceaccount kueue-ks-controller-manager -n kueue-ks-system --ignore-not-found
kubectl delete clusterrole kueue-ks-kueue-ks-editor-role --ignore-not-found
kubectl delete clusterrole kueue-ks-manager-role --ignore-not-found
kubectl delete clusterrole kueue-ks-metrics-reader --ignore-not-found
kubectl delete role kueue-ks-leader-election-role -n kueue-ks-system --ignore-not-found
kubectl delete clusterrolebinding kueue-ks-manager-rolebinding --ignore-not-found
kubectl delete clusterrolebinding kueue-ks-proxy-rolebinding --ignore-not-found
kubectl delete service kueue-ks-controller-manager-metrics-service -n kueue-ks-system --ignore-not-found
kubectl delete clusterrole kueue-ks-proxy-role --ignore-not-found
kubectl delete ns kueue-ks-system --ignore-not-found

: Deleting cluster metrics controller
if ! output=$( helm --kube-context wds2 delete cluster-metrics --ignore-not-found  2>&1); then
   echo "" >&2
fi    


#sleep 5

for cluster in "${clusters[@]}"; do    
  kubectl --context $cluster delete clusterroles appwrappers-access --ignore-not-found
  kubectl --context $cluster delete clusterrolebindings klusterlet-appwrappers-access --ignore-not-found
  kubectl --context ${cluster} delete -f https://raw.githubusercontent.com/project-codeflare/appwrapper/main/config/crd/bases/workload.codeflare.dev_appwrappers.yaml --ignore-not-found
  kubectl --context ${cluster} delete -f https://raw.githubusercontent.com/kubestellar/galaxy/main/clustermetrics/config/crd/bases/galaxy.kubestellar.io_clustermetrics.yaml --ignore-not-found
  kubectl --context ${cluster} delete -k github.com/kubeflow/training-operator/manifests/overlays/standalone?ref=v1.5.0 --ignore-not-found

  kubectl --context ${cluster} delete ns clustermetrics-system --ignore-not-found
  
done
# 11 kubectl --context kubeflex delete -f ${SCRIPT_DIR}/../kueue/templates/transform-pytorch-job.yaml --ignore-not-found
kubectl --context kubeflex delete -f ${SCRIPT_DIR}/templates/transform-pytorch-job.yaml --ignore-not-found

kubectl --context kubeflex delete -f https://raw.githubusercontent.com/project-codeflare/appwrapper/main/config/crd/bases/workload.codeflare.dev_appwrappers.yaml --ignore-not-found
kubectl --context kubeflex delete -f https://raw.githubusercontent.com/kubestellar/galaxy/main/clustermetrics/config/crd/bases/galaxy.kubestellar.io_clustermetrics.yaml --ignore-not-found
kubectl --context kubeflex delete -f https://raw.githubusercontent.com/kubeflow/training-operator/855e0960668b34992ba4e1fd5914a08a3362cfb1/manifests/base/crds/kubeflow.org_pytorchjobs.yaml --ignore-not-found
kubectl --context kubeflex delete ns clustermetrics-system --ignore-not-found

kubectl --context kubeflex delete clusterrolebinding kubeflex-manager-cluster-admin-rolebinding --ignore-not-found

helm --kube-context kubeflex uninstall -n wds2-system kubestellar --ignore-not-found


if ! output=$( kflex delete wds2  2>&1); then
   echo "" >&2
fi

sleep 15

: ----------------- INSTALLING -----------------

kubectl --context kubeflex apply -f https://raw.githubusercontent.com/kubeflow/training-operator/855e0960668b34992ba4e1fd5914a08a3362cfb1/manifests/base/crds/kubeflow.org_pytorchjobs.yaml

: Installing kueue controller
kubectl --context kubeflex apply --server-side -f https://github.com/kubernetes-sigs/kueue/releases/download/$KUEUE_VERSION/manifests.yaml

: waiting for kueue controller to come up 
wait-for-cmd '(($(wrap-cmd kubectl --context kubeflex get deployments -n kueue-system -o jsonpath='{.status.readyReplicas}' kueue-controller-manager 2>/dev/null || echo 0) >= 1))'

kubectl --context kubeflex create -f ${SCRIPT_DIR}/templates/admissioncheck-ks.yaml
kubectl --context kubeflex create -f ${SCRIPT_DIR}/templates/user-queue-ks.yaml
kubectl --context kubeflex create -f ${SCRIPT_DIR}/templates/spot-resource-flavor.yaml
kubectl --context kubeflex create -f ${SCRIPT_DIR}/templates/default-flavor.yaml
kubectl --context kubeflex create -f ${SCRIPT_DIR}/templates/zero-cluster-queue-ks.yaml


: Installing ClusterRoleBinding

kubectl --context kubeflex apply -f - <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kubeflex-manager-cluster-admin-rolebinding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-admin
subjects:
- kind: ServiceAccount
  name: kubeflex-controller-manager
  namespace: kubeflex-system
EOF

: Installing wds2 of type 'host'

kflex create wds2 -t host

helm --kube-context kubeflex upgrade --install ocm-transport-plugin oci://ghcr.io/kubestellar/ocm-transport-plugin/chart/ocm-transport-plugin --version ${OCM_TRANSPORT_PLUGIN} \
    --set transport_cp_name=its1 \
    --set wds_cp_name=wds2 \
    -n wds2-system

: Label the wds2 control plane as type wds:

kubectl label cp wds2 kflex.kubestellar.io/cptype=wds

: Deploy clustermetrics to  each Cluster
: install cluster-metrics controller on all clusters

cd ${SCRIPT_DIR}/../../clustermetrics

make ko-local-build

kubectl --context wds2 apply -f config/crd/bases

contexts=(cluster1 cluster2);
for context in "${contexts[@]}"; do
    kubectl --context ${context} apply -f config/crd/bases 
    kubectl --context ${context} apply -f ${SCRIPT_DIR}/../common/templates/cluster-metrics-rbac.yaml
    cluster=${context}
    CONTEXT=${context} CLUSTER=${cluster} HELM_OPTS="--set clusterName=${cluster}" make k3d-install-local-chart

    kubectl --context ${context} apply -f https://raw.githubusercontent.com/project-codeflare/appwrapper/main/config/crd/bases/workload.codeflare.dev_appwrappers.yaml
    kubectl --context ${context} apply -f https://raw.githubusercontent.com/kubestellar/galaxy/main/clustermetrics/config/crd/bases/galaxy.kubestellar.io_clustermetrics.yaml
    kubectl --context ${context} apply -k "github.com/kubeflow/training-operator/manifests/overlays/standalone?ref=v1.7.0"
    kubectl --context wds2 apply -f ${SCRIPT_DIR}/../common/templates/cluster-metrics-${cluster}.yaml
    kubectl --context wds2 apply -f ${SCRIPT_DIR}/templates/binding-policy-${cluster}.yaml
    
done

kubectl --context wds2 apply -f https://raw.githubusercontent.com/project-codeflare/appwrapper/main/config/crd/bases/workload.codeflare.dev_appwrappers.yaml
kubectl --context wds2 apply -f https://raw.githubusercontent.com/kubestellar/galaxy/main/clustermetrics/config/crd/bases/galaxy.kubestellar.io_clustermetrics.yaml


kubectl --context kubeflex create -f ${SCRIPT_DIR}/templates/transform-pytorch-job.yaml

cd ${SCRIPT_DIR}

: Apply the kubestellar controller-manager helm chart

helm --kube-context kubeflex upgrade --install -n wds2-system kubestellar oci://ghcr.io/kubestellar/kubestellar/controller-manager-chart --version ${KUBESTELLAR_VERSION} --set ControlPlaneName=wds2 --set APIGroups="workload.codeflare.dev\,workloads.kueue.x-k8s.io\,batch\,galaxy.kubestellar.io\,kubeflow.org"

kubectl config use-context kubeflex

for cluster in "${clusters[@]}"; do
kubectl --context ${cluster} apply -f - <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: appwrappers-access
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

#kubectl --context wds2 create -f ${SCRIPT_DIR}/../kueue/templates/admissioncheck-ks.yaml
#kubectl --context wds2 create -f ${SCRIPT_DIR}/../kueue/templates/user-queue-ks.yaml
#kubectl --context wds2 create -f ${SCRIPT_DIR}/../kueue/templates/default-flavor.yaml
#kubectl --context wds2 create -f ${SCRIPT_DIR}/../kueue/templates/zero-cluster-queue-ks.yaml

cd ${SCRIPT_DIR}/../../kueue-ks

make ko-local-build

#cd ${SCRIPT_DIR}/../../charts/kueue-ks

CONTEXT=kubeflex HELM_OPTS="--set queueName=cluster-queue-ks" make k3d-install-local-chart
