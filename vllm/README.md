### Overview
![Overview of the setup.](./overview.svg)

### Steps
```shell
kind load --name cluster1 docker-image vllm-cpu-env:latest

kubectl --context wds1 apply -f ./vllm/deployment_gpt2.yaml
kubectl --context wds1 apply -f ./vllm/bp_gpt2.yaml
kubectl --context cluster1 get all
kubectl --context cluster1 wait --for=condition=available --timeout=90s deployment/gpt2
kubectl --context cluster1 get all

docker exec -it cluster1-control-plane bash
curl -s <pod-ip>:8000/v1/models | jq

kubectl --context wds1 apply -f ./vllm/service_gpt2.yaml
kubectl --context cluster1 edit svc gpt2 # nodeport should be 30081
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

kind load --name cluster2 docker-image vllm-cpu-env:latest

kubectl --context wds1 apply -f ./vllm/deployment_gpt2-pmc.yaml
kubectl --context wds1 apply -f ./vllm/bp_gpt2-pmc.yaml
kubectl --context cluster2 get all
kubectl --context cluster2 wait --for=condition=available --timeout=90s deployment/gpt2-pmc
kubectl --context cluster2 get all

docker exec -it cluster2-control-plane bash
curl -s <pod-ip>:8000/v1/models | jq

kubectl --context wds1 apply -f ./vllm/service_gpt2-pmc.yaml
kubectl --context cluster2 edit svc gpt2-pmc # nodeport should be 30082
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