#!/bin/bash

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

kind delete clusters kubeflex cluster1 cluster2
cp ~/.kube/config ~/.kube/config.bak || true
rm ~/.kube/config || true

: create kubeflex instamce

kflex init --create-kind

: install post-create-hooks

kubectl apply -f ${SCRIPT_DIR}/templates/kubestellar.yaml
kubectl apply -f ${SCRIPT_DIR}/templates/ocm.yaml

: elevate permissions for kubeflex controller

kubectl --context kind-kubeflex apply -f ${SCRIPT_DIR}/templates/ks-rbac.yaml

: create its1 of type host

kflex create its1 -t host -p ocm

: wait for OCM cluster manager up

wait-for-cmd '(($(wrap-cmd kubectl --context its1 get deployments.apps -n open-cluster-management -o jsonpath='{.status.readyReplicas}' cluster-manager 2>/dev/null || echo 0) >= 1))'

: create clusters and register

flags="--force-internal-endpoint-lookup"
for cluster in "${clusters[@]}"; do
    kind create cluster --name ${cluster}
    kubectl config rename-context kind-${cluster} ${cluster}
    clusteradm --context its1 get token | grep '^clusteradm join' | sed "s/<cluster_name>/${cluster}/" | awk '{print $0 " --context '${cluster}' --singleton '${flags}'"}' | sh
done

: Wait for csrs in its1
wait-for-cmd '(($(kubectl --context its1 get csr 2>/dev/null | grep -c Pending) >= 2))'

: accept csr

clusteradm --context its1 accept --clusters cluster1
clusteradm --context its1 accept --clusters cluster2

: label clusters

kubectl --context its1 get managedclusters
for cluster in "${clusters[@]}"; do
    kubectl --context its1 label managedcluster ${cluster} location-group=edge name=${cluster}
done

: create wds0 of type host

kubectl config use-context kind-kubeflex
kflex create wds0 -t host -p kubestellar --set itsName=its1

wait-for-cmd '(($(wrap-cmd kubectl --context wds0 get deployments.apps -n wds0-system -o jsonpath='{.status.readyReplicas}' kubestellar-controller-manager 2>/dev/null || echo 0) >= 1))'

: kubestellar-controller-manager is running

wait-for-cmd '(($(wrap-cmd kubectl --context wds0 get deployments.apps -n wds0-system -o jsonpath='{.status.readyReplicas}' transport-controller 2>/dev/null || echo 0) >= 1))'

: transport controller is running

 




