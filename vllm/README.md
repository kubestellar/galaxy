### Overview
This is an experiment to use vLLM as KuberStellar's workloads.

Below is a simplified overview of the experiment.
The overview shows one KubeStellar WDS, wds1; and two KubeStelalr WECs, cluster1 and cluster2.
KuberStellar has a nice [Getting Started](https://docs.kubestellar.io/unreleased-development/direct/get-started/) guide on how to setup such an envrionment.

Two configuration files for the two WECs are available here as [cluster1.yaml](./cluster1.yaml) and [cluster2.yaml](./cluster2.yaml), if one wants to conveniently access the Kubernetes Services inside the WECs, please create the WECs from the configuration files.
```shell
kind create cluster --config=./vllm/cluster1.yaml
kind create cluster --config=./vllm/cluster2.yaml
```

![Overview of the setup.](./overview.svg)

In this experiment, two instances of vLLM (as the two Deployments) will be dispatched by KubeStellar to the two WECs.

### Steps

To stay simple, this experiment use CPUs (instead of GPUs). The vLLM project offers Dockerfiles to build 'CPU images'. Please refer to [vLLM's docs](https://docs.vllm.ai/en/latest/getting_started/installation/cpu/index.html#) on how to build a CPU image.

Once the CPU image is built, load it into the WECs.
```shell
kind load --name cluster1 docker-image vllm-cpu-env:latest
kind load --name cluster2 docker-image vllm-cpu-env:latest
```

Create Deployment and BindingPolicy for the first vLLM instance, in wds1.
Then wait for the Deployment to be available, in cluster1.
```shell
kubectl --context wds1 apply -f ./vllm/deployment_gpt2.yaml
kubectl --context wds1 apply -f ./vllm/bp_gpt2.yaml
kubectl --context cluster1 wait --for=condition=available --timeout=90s deployment/gpt2
kubectl --context cluster1 get all
```

Expose the Deployment as a Service.
Then edit the Service's nodePort.
```shell
kubectl --context wds1 apply -f ./vllm/service_gpt2.yaml
kubectl --context cluster1 edit svc gpt2 # nodeport should be 30081
```

List the model.
```shell
curl -s http://localhost:30081/v1/models | jq
curl -s http://localhost:30081/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
        "model": "openai-community/gpt2",
        "prompt": "IBM is a",
        "max_tokens": 17,
        "temperature": 0
      }' \
  | jq
```

Inference.
```shell
curl -s http://localhost:30081/v1/models | jq
curl -s http://localhost:30081/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
        "model": "openai-community/gpt2",
        "prompt": "IBM is a",
        "max_tokens": 17,
        "temperature": 0.2
      }' \
  | jq .choices[0].text
```

Take similar steps to dispatch the second vLLM instance to cluster2.
```shell
kubectl --context wds1 apply -f ./vllm/deployment_gpt2-pmc.yaml
kubectl --context wds1 apply -f ./vllm/bp_gpt2-pmc.yaml
kubectl --context cluster2 wait --for=condition=available --timeout=90s deployment/gpt2-pmc
kubectl --context cluster2 get all

kubectl --context wds1 apply -f ./vllm/service_gpt2-pmc.yaml
kubectl --context cluster2 edit svc gpt2-pmc # nodeport should be 30082
```

List model and inference.
```shell
curl -s http://localhost:30082/v1/models | jq
curl -s http://localhost:30082/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
        "model": "manupande21/GPT2_PMC",
        "prompt": "IBM is a",
        "max_tokens": 17,
        "temperature": 0
      }' \
  | jq

curl -s http://localhost:30082/v1/models | jq
curl -s http://localhost:30082/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
        "model": "manupande21/GPT2_PMC",
        "prompt": "IBM is a",
        "max_tokens": 17,
        "temperature": 0.2
      }' \
  | jq .choices[0].text
```

Tearing down.
```shell
kind delete cluster --name cluster1
kind delete cluster --name cluster2
```
