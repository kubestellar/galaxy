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

# Usage: source this file, in a bash shell

# wait-for-cmd concatenates its arguments into one command that is iterated
# until it succeeds, failing if there is not success in 3 minutes.
# Note that the technology used here means that word splitting is done on
# the concatenation, and any quotes used by the caller to surround words
# do not get into here.
function wait-for-cmd() (
    cmd="$@"
    wait_counter=0
    while ! (eval "$cmd") ; do
        if (($wait_counter > 100)); then
            echo "Failed to ${cmd}."
            exit 1
        fi
        ((wait_counter += 1))
        sleep 5
    done
)

export -f wait-for-cmd



# expect-cmd-output takes two arguments:
# - a command to execute to produce some output, and
# - a command to test that output (received on stdin).
# expect-cmd-output executes the first command,
# echoes the output, and then applies the test.
function expect-cmd-output() {
    out=$(eval "$1")
    echo "$out"
    echo "$out" | $(eval "$2")
}

export -f expect-cmd-output

function wrap-cmd() {
    output=$($@) || echo 0
    [[ -z "$output" ]] && output=0
    echo "$output"
}

export -f wrap-cmd


function wait-for-deployment() {

if [ "$#" -ne 3 ]; then
  echo "Usage: $0 <context> <namespace> <deployment-name>"
  exit 1
fi

CONTEXT=$1
NAMESPACE=$2
DEPLOYMENT_NAME=$3
SLEEP_SECONDS=3 # How long to sleep between checks

while :; do
    # Check if the deployment exists
    if kubectl --context ${CONTEXT} get deployment "$DEPLOYMENT_NAME" -n "$NAMESPACE" > /dev/null 2>&1; then
        echo "Checking deployment $DEPLOYMENT_NAME..."

        # Retrieve the number of ready replicas and desired replicas for the deployment
        READY_REPLICAS=$(kubectl  --context ${CONTEXT} get deployment "$DEPLOYMENT_NAME" -n "$NAMESPACE" -o jsonpath='{.status.readyReplicas}')
        REPLICAS=$(kubectl  --context ${CONTEXT} get deployment "$DEPLOYMENT_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.replicas}')

        # Check if READY_REPLICAS is unset or empty, setting to zero if it is
        [ -z "$READY_REPLICAS" ] && READY_REPLICAS=0

        # Compare the number of ready replicas with the desired number of replicas
        if [ "$READY_REPLICAS" -eq "$REPLICAS" ]; then
            echo "All replicas are ready."
            break
        else
            echo "Ready replicas ($READY_REPLICAS) do not match the desired count of replicas ($REPLICAS)."
        fi
    else
        echo "Deployment $DEPLOYMENT_NAME does not exist in the namespace $NAMESPACE and context $CONTEXT"
    fi

    # Sleep before checking again
    echo "Sleeping for $SLEEP_SECONDS seconds..."
    sleep "$SLEEP_SECONDS"
done
}

export -f wait-for-deployment

wait-for-apiresource() {
    local context="$1"
    local api_group="$2"
    local resource_name="$3"
    local interval_seconds=3

    echo "Polling for Kubernetes resource '$resource_name' in API group '$api_group' every $interval_seconds seconds."

    while true; do
        # Get the list of all resources within the specified API group and check if the specified resource name is present
        if kubectl --context ${core} api-resources --api-group="$api_group" | awk '{print $1}' | tail -n +2 | grep -q "^$resource_name$"; then
            echo "Kubernetes resource '$resource_name' in API group '$api_group' has been found."
            return 0
        else
            echo "Kubernetes resource '$resource_name' in API group '$api_group' not found yet. Retrying in $interval_seconds seconds..."
            sleep "$interval_seconds"
        fi
    done
}

export -f wait-for-apiresource

# waits until all the deployments for a given context and namespace are ready
wait-for-all-deployments-in-namespace() {
    context=$1
    namespace=$2

    local allReady=true
    deployments=$(kubectl --context ${context} get deployments -n ${namespace} -o jsonpath='{.items[*].metadata.name}')
    for deployment in $deployments; do
        readyReplicas=$(kubectl --context ${context} get deployment $deployment -n ${namespace} -o jsonpath='{.status.readyReplicas}')
        if [[ "$readyReplicas" -lt 1 ]]; then
            allReady=false
            break
        fi
    done
    echo $allReady
}

export -f wait-for-all-deployments-in-namespace

# copies a secret from a source namespace namespace to a target namespace
copy-secret() {
    # Check for correct number of arguments
    if [ "$#" -ne 4 ]; then
        echo "Usage: $0 <context> <source-namespace> <secret-name> <target-namespace>"
        exit 1
    fi

    context=$1
    SOURCE_NAMESPACE=$2
    SECRET_NAME=$3
    TARGET_NAMESPACE=$4

    kubectl get secret $SECRET_NAME --namespace $SOURCE_NAMESPACE -o yaml | \
    sed -e 's/namespace: .*//g' \
    -e '/^  ownerReferences:/,/^  creationTimestamp:/d' | \
    kubectl apply --namespace=$TARGET_NAMESPACE -f -
}

export -f copy-secret
