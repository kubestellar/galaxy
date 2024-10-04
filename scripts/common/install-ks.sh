#!/bin/bash

set -x # echo so that users can understand what is happening
set -e # exit on error

export KUBESTELLAR_VERSION=0.24.0

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
source "${SCRIPT_DIR}/setup-shell.sh"
source "${SCRIPT_DIR}/config.sh"

kind delete clusters kubeflex cluster1 cluster2
cp ~/.kube/config ~/.kube/config.bak || true
rm ~/.kube/config || true

: create kubeflex cluster
bash <(curl -s https://raw.githubusercontent.com/kubestellar/kubestellar/v0.24.0/scripts/create-kind-cluster-with-SSL-passthrough.sh) --name kubeflex --port 9443

: install kubestellar and create the WDS and ITS
helm upgrade --install ks-core oci://ghcr.io/kubestellar/kubestellar/core-chart \
   --version $KUBESTELLAR_VERSION \
   --set-json='ITSes=[{"name":"its1", "type":"host"}]' \
   --set-json='WDSes=[{"name":"wds0", "type":"host"}]'

kubectl config get-contexts

: wait for OCM cluster manager up

wait-for-cmd '(($(wrap-cmd kubectl --context kind-kubeflex get deployments.apps -n open-cluster-management -o jsonpath='{.status.readyReplicas}' cluster-manager 2>/dev/null || echo 0) >= 1))'

: create clusters and register

flags="--force-internal-endpoint-lookup"
for cluster in "${clusters[@]}"; do
    kind create cluster --name ${cluster}
    kubectl config rename-context kind-${cluster} ${cluster}
    clusteradm --context kind-kubeflex get token | grep '^clusteradm join' | sed "s/<cluster_name>/${cluster}/" | awk '{print $0 " --context '${cluster}' --singleton '${flags}'"}' | sh
done

: Wait for csrs
wait-for-cmd '(($(kubectl --context kind-kubeflex get csr 2>/dev/null | grep -c Pending) >= 2))'

: accept csrs

clusteradm --context kind-kubeflex accept --clusters cluster1
clusteradm --context kind-kubeflex accept --clusters cluster2

: label clusters

kubectl --context kind-kubeflex get managedclusters
for cluster in "${clusters[@]}"; do
    kubectl --context kind-kubeflex label managedcluster ${cluster} location-group=edge name=${cluster}
done
