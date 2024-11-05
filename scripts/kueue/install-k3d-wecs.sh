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

wait_for_csrs() {
    local context="$1"
    local desired_csrs="$2"

    if [ "$#" -ne 2 ]; then
        echo "Usage: $0 <context> <desired_csrs>"
        return 1
    fi

    echo "Waiting for ${desired_csrs} csrs to show up..."
    while true; do
        csrs=$(kubectl --context ${context} get csr --no-headers | wc -l | awk '{$1=$1};1')

        if [ "${csrs}" == "${desired_csrs}" ]; then
            echo "${desired_csrs} are present"
            break
        fi

        echo -n "."
        sleep 2
    done
}

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &> /dev/null && pwd)
source "${SCRIPT_DIR}/../common/setup-shell.sh"
source "${SCRIPT_DIR}/../common/config.sh"

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

#: Wait for csrs in its1
#wait-for-cmd '(($(kubectl --context its1 get csr 2>/dev/null | grep -c Pending) >= 2))'
: wait for 2 csrs to show up on the hub

wait_for_csrs its1 2

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
