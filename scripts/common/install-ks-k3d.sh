#!/bin/bash

# IMPORTANT WHEN RUNNING WITH RANCHER DESKTOP
#
# During the install documented below you may run out of quota for open files/inotify
# To prevent the problem from happening create this file:
#
# touch ~/Library/Application\ Support/rancher-desktop/lima/_config/override.yaml
#
# and add contents from https://github.com/kubestellar/galaxy/tree/main/scripts/kfp#preparation
#
# Restart Rancher Desktop for changes to take effect
#
set -x # echo so that users can understand what is happening
set -e # exit on error

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
source "${SCRIPT_DIR}/setup-shell.sh"
source "${SCRIPT_DIR}/config.sh"

export KUBESTELLAR_VERSION=0.22.0
export OCM_STATUS_ADDON_VERSION=0.2.0-rc8
export OCM_TRANSPORT_PLUGIN=0.1.7

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

k3d cluster delete kubeflex cluster1 cluster2
cp ~/.kube/config ~/.kube/config.bak || true
rm ~/.kube/config || true
touch ~/.kube/config || true

k3d cluster create -p "9443:443@loadbalancer" --k3s-arg "--disable=traefik@server:*" kubeflex

helm install ingress-nginx ingress-nginx --set "controller.extraArgs.enable-ssl-passthrough=" --repo https://kubernetes.github.io/ingress-nginx --version 4.6.1 --namespace ingress-nginx --create-namespace

docker stop k3d-kubeflex-server-0
docker rename k3d-kubeflex-server-0 kubeflex-control-plane
docker start kubeflex-control-plane


: Wait for all pods to be restarted in k3d-kubeflex
wait-for-cmd '(($(kubectl --context k3d-kubeflex get po -A 2>/dev/null | grep -c Running) >= 5))'

: pods above first get into Running state but then Fail and Restart. Wait and watch again
sleep 20

: Wait for all pods to be restarted in k3d-kubeflex
wait-for-cmd '(($(kubectl --context k3d-kubeflex get po -A 2>/dev/null | grep -c Running) >= 5))'

: initialize kubeflex and create the its1 space with OCM running in it:
kflex init
kubectl apply -f https://raw.githubusercontent.com/kubestellar/kubestellar/v${KUBESTELLAR_VERSION}/config/postcreate-hooks/kubestellar.yaml
kubectl apply -f https://raw.githubusercontent.com/kubestellar/kubestellar/v${KUBESTELLAR_VERSION}/config/postcreate-hooks/ocm.yaml
kubectl config rename-context k3d-kubeflex kubeflex
kflex create its1 --type vcluster -p ocm

: wait for OCM cluster manager up

wait-for-cmd '(($(wrap-cmd kubectl --context its1 get deployments.apps -n open-cluster-management -o jsonpath='{.status.readyReplicas}' cluster-manager 2>/dev/null || echo 0) >= 1))'


: Install OCM status addon
helm --kube-context its1 upgrade --install status-addon -n open-cluster-management --create-namespace  oci://ghcr.io/kubestellar/ocm-status-addon-chart --version v${OCM_STATUS_ADDON_VERSION}

: Create a Workload Description Space wds1 in KubeFlex
kflex create wds1 -p kubestellar

: Deploy the OCM based transport controller
helm --kube-context kubeflex upgrade --install ocm-transport-plugin oci://ghcr.io/kubestellar/ocm-transport-plugin/chart/ocm-transport-plugin --version ${OCM_TRANSPORT_PLUGIN} \
 --set transport_cp_name=its1 \
 --set wds_cp_name=wds1 \
 -n wds1-system

: create clusters and register

flags="--force-internal-endpoint-lookup"
cluster_port=31080
for cluster in "${clusters[@]}"; do
    k3d cluster create -p "${cluster_port}:80@loadbalancer"  --network k3d-kubeflex ${cluster}
: Renaming new cluster as ${cluster}
    kubectl config rename-context k3d-${cluster} ${cluster}
: Registering ${cluster}    
    clusteradm --context its1 get token | grep '^clusteradm join' | sed "s/<cluster_name>/${cluster}/" | awk '{print $0 " --context '${cluster}' --singleton '${flags}'"}' | sh
: Done
    (( cluster_port += 100))
done

: Waiting for clusters csr to reach Pending state

: Wait for csrs in its1
wait-for-cmd '(($(kubectl --context its1 get csr 2>/dev/null | grep -c Pending) >= 2))'

: accept csr

clusteradm --context its1 accept --clusters cluster1

: Confirm cluster1 is accepted and label it for the BindingPolicy

kubectl --context its1 get managedclusters
kubectl --context its1 label managedcluster cluster1 location-group=edge name=cluster1

clusteradm --context its1 accept --clusters cluster2

: Confirm cluster2 is accepted and label it for the BindingPolicy

kubectl --context its1 get managedclusters
kubectl --context its1 label managedcluster cluster2 location-group=edge name=cluster2

: Kubestellar has been installed on your K3D
