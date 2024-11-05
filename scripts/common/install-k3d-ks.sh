#!/bin/bash

# this script installs control cluster (the hub) and kubestellar
#
set -x # echo so that users can understand what is happening
set -e # exit on error

#export KUBESTELLAR_VERSION=0.24.0
export KUBESTELLAR_VERSION=0.25.0-rc.1

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

: wait for OCM cluster manager up

wait-for-cmd '(($(wrap-cmd kubectl --context its1 get deployments.apps -n open-cluster-management -o jsonpath='{.status.readyReplicas}' cluster-manager 2>/dev/null || echo 0) >= 1))'
