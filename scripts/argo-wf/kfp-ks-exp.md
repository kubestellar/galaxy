# Multicluster Argo Workflows with Kubestellar

Initial focus is on KFP v2, which runs on Argo Workflows, so we will focus on that only to start.

## Prereqs

- KubeFlex v0.5.1
- kubectl
- go
- make
- helm


## Argo Workflows

1. Deploy KubeStellar (testing from main)
2. Create a wds0 of type host
2.1 Update chart for ks from forked repo:

```shell
make ko-build-local
make kind-load-image
make local-chart
helm delete -n wds0-system kubestellar 
helm upgrade --kube-context kind-kubeflex --install kubestellar -n wds0-system ./local-chart  --set ControlPlaneName=wds0
```

3. Deploy latest Argo Workflows on all clusters
```shell
clusters=(wds0 cluster1 cluster2);
  for cluster in "${clusters[@]}"; do
  kubectl --context ${cluster} create namespace argo
  kubectl --context ${cluster} apply -n argo -f https://github.com/argoproj/argo-workflows/releases/download/v3.5.5/quick-start-minimal.yaml
done
```
3.1 Deploy RBAC for workflows on all clusters 
```shell
clusters=(cluster1 cluster2);
for cluster in "${clusters[@]}"; do
kubectl --context ${cluster} apply -f - <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: klusterlet-argo-access
rules:
- apiGroups: ["argoproj.io"]
  resources: ["workflows"]
  verbs: ["get", "list", "watch", "create", "update", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: klusterlet-argo-access
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: klusterlet-argo-access
subjects:
- kind: ServiceAccount
  name: klusterlet-work-sa
  namespace: open-cluster-management-agent
EOF
done
```

4. Install mutating adimission webhook

```shell
make ko-local-build && make install-local-chart
```

5. Deploy a workflow on wds0
```shell
kubectl --context wds0 apply -f - <<EOF
apiVersion: argoproj.io/v1alpha1
kind: Workflow
metadata:
  name: hello-world-abcd3
  namespace: argo
  labels:
    workflows.argoproj.io/archive-strategy: "false"
  annotations:
    workflows.argoproj.io/description: |
      This is a simple hello world example.
spec:
  entrypoint: whalesay
  templates:
  - name: whalesay
    container:
      image: docker/whalesay:latest
      command: [cowsay]
      args: ["hello world"]
EOF
```
6. Apply a binding policy that matches workflows:

```shell
kubectl --context wds0 apply -f - <<EOF
apiVersion: control.kubestellar.io/v1alpha1
kind: BindingPolicy
metadata:
  name: workflows
spec:
  wantSingletonReportedState: true
  clusterSelectors:
  - matchLabels:
      name: cluster1
  downsync:
  - apiGroup: argoproj.io
    resources: [ workflows ]
    namespaceSelectors: []
EOF    
``` 

7. Clone the forked transport plugin:

```shell
git clone https://github.com/pdettori/ocm-transport-plugin.git
cd ocm-transport-plugin
```
TODO - need also a forked repo for KS + replace directive

8. Clone & Run transport plugin

Clone:

```shell
git clone https://github.com/kubestellar/ocm-transport-plugin.git
cd ocm-transport-plugin
git checkout local
: make sure to edit go.mod and adjust your replace directive to point to local ks repo
```

Build and run in a shell:

```
make build
./bin/ocm-transport-plugin --transport-context its1 --wds-context wds0 --wds-name wds0 -v=2
```




## Discussion

One approach to integrate is to use tolerations:

1.Directly Specifying Tolerations in the Workflow YAML

2. Utilizing KFP's containerOp with Spec Override

KFP provides the containerOp component, which allows defining container execution within the pipeline. You can leverage the spec property within containerOp to override the default pod spec and add tolerations.

```python
import kfp.v2 as v2

def main_step(name):
  # ... container implementation

tolerations = [v2.ContainerOp. toleration(key="my-node-type", operator="Equal", value="unhealthy", effect="NoSchedule")]

v2.api_v2.ContainerOp(
  name="main-step",
  container=v2.ContainerSpec(image="...", name="...", command=["..."]),
  spec=v2.pod_spec(tolerations=tolerations),
).apply(arguments={"name": "my-work"})
```

3. Use suspend in Argo workflow and sync workflow:
Example:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Workflow
metadata:
  annotations:
    workflows.argoproj.io/description: |
      This is a simple hello world example.
    workflows.argoproj.io/pod-name-format: v2
  labels:
    workflows.argoproj.io/archive-strategy: "false"
    workflows.argoproj.io/completed: "false"
    workflows.argoproj.io/phase: Running
  name: hello-world-abcd3
  namespace: argo
spec:
  arguments: {}
  entrypoint: whalesay
  suspend: true
  templates:
  - container:
      args:
      - hello world
      command:
      - cowsay
      image: docker/whalesay:latest
      name: ""
      resources: {}
    inputs: {}
    metadata: {}
    name: whalesay
    outputs: {}
```
## Hacking around

### Expose the grpc metadata service in kubeflex-control-plane as nodeport and point to that from cluster1

```shell
k --context cluster1 edit cm -n kubeflow metadata-grpc-configmap 
```

change 

```
 METADATA_GRPC_SERVICE_HOST: metadata-grpc-service
 METADATA_GRPC_SERVICE_PORT: "8080"
```  