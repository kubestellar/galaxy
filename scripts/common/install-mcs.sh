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
source "${SCRIPT_DIR}/setup-shell.sh"
source "${SCRIPT_DIR}/config.sh"


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

: install cluster-metrics controller on all clusters

cd ${SCRIPT_DIR}/../../clustermetrics
make ko-local-build

contexts=("${clusters[@]}")
contexts+=("kind-kubeflex")
for context in "${contexts[@]}"; do
    clusterName=${context}
    cluster=${context}
    if [[ ${context} == "kind-kubeflex" ]]; then
      clusterName="local"
      cluster="kubeflex"
    fi
    CONTEXT=${context} CLUSTER=${cluster} HELM_OPTS="--set clusterName=${clusterName}" make install-local-chart 
    kubectl --context ${context} apply -f ${SCRIPT_DIR}/templates/cluster-metrics-rbac.yaml
done

: deploy cluster-metrics objects for each cluster

clusters+=("kind-kubeflex")
for cluster in "${clusters[@]}"; do
    kubectl --context kind-kubeflex apply -f ${SCRIPT_DIR}/templates/cluster-metrics-${cluster}.yaml
done

: install mc-scheduler on core cluster

cd ${SCRIPT_DIR}/../../mc-scheduling
kubectl config use-context kind-kubeflex
make ko-local-build
CLUSTER=kubeflex make install-local-chart
