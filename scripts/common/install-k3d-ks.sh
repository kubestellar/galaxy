#!/bin/bash

set -x # echo so that users can understand what is happening
set -e # exit on error

export KUBESTELLAR_VERSION=0.24.0

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
source "${SCRIPT_DIR}/setup-shell.sh"
source "${SCRIPT_DIR}/config.sh"

k3d cluster delete kubeflex cluster1 cluster2
cp ~/.kube/config ~/.kube/config.bak || true
rm ~/.kube/config || true

k3d cluster create -p "9443:443@loadbalancer" --k3s-arg "--disable=traefik@server:*" kubeflex

helm install ingress-nginx ingress-nginx --set "controller.extraArgs.enable-ssl-passthrough=" --repo https://kubernetes.github.io/ingress-nginx --version 4.6.1 --namespace ingress-nginx --create-namespace

helm upgrade --install ks-core oci://ghcr.io/kubestellar/kubestellar/core-chart     --version $KUBESTELLAR_VERSION     --set-json='ITSes=[{"name":"its1"}]'     --set-json='WDSes=[{"name":"wds1"},{"name":"wds2", "type":"host"}]' --set "kubeflex-operator.hostContainer=k3d-kubeflex-server-0"
# this may take a min or two ...
kflex ctx its1
kflex ctx wds1
kflex ctx wds2
kubectl config get-contexts

k3d cluster create   --network k3d-kubeflex cluster1
sleep 15
kubectl config rename-context k3d-cluster1 cluster1
flags="--force-internal-endpoint-lookup"
clusteradm --context its1 get token | grep '^clusteradm join' | sed "s/<cluster_name>/cluster1/" | awk '{print $0 " --context 'cluster1' --singleton '${flags}'"}' | sh

sleep 15
k3d cluster create   --network k3d-kubeflex cluster2
sleep 15

kubectl config rename-context k3d-cluster2 cluster2
flags="--force-internal-endpoint-lookup"
clusteradm --context its1 get token | grep '^clusteradm join' | sed "s/<cluster_name>/cluster2/" | awk '{print $0 " --context 'cluster2' --singleton '${flags}'"}' | sh

sleep 15
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

kubectl config use-context k3d-kubeflex

k3d node create standard-00 --cluster cluster2 --role agent  --k3s-node-label instance=h100
k3d node create spot-00 --cluster cluster1 --role agent  --k3s-node-label instance=spot
