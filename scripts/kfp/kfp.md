# KFP integrations

## Installation on kind

Install [kfp v2 on kind](https://www.kubeflow.org/docs/components/pipelines/v1/installation/localcluster-deployment/):

```shell
export PIPELINE_VERSION=2.0.5
kubectl apply -k "github.com/kubeflow/pipelines/manifests/kustomize/cluster-scoped-resources?ref=$PIPELINE_VERSION"
kubectl wait --for condition=established --timeout=60s crd/applications.app.k8s.io
kubectl apply -k "github.com/kubeflow/pipelines/manifests/kustomize/env/platform-agnostic-pns?ref=$PIPELINE_VERSION"
```

Note: it may take up to 10 minutes to get all pods in a running state.

Very important: you need to switch to the [Emissary Executor](https://www.kubeflow.org/docs/components/pipelines/v1/installation/choose-executor/#migrate-to-emissary-executor) to run correctly v2 pipelines. By default KFP
installs an executor that is not supported. See [this KFP issue](https://github.com/kubeflow/pipelines/issues/9119)

```shell
kubectl -n kubeflow patch configmap workflow-controller-configmap --patch '{"data":{"containerRuntimeExecutor":"emissary"}}'
kubectl apply -k "github.com/kubeflow/pipelines/manifests/kustomize/env/platform-agnostic-emissary?ref=$PIPELINE_VERSION"
```

Install the [SDK](https://www.kubeflow.org/docs/components/pipelines/v1/sdk/install-sdk/):

on MacOS
```
brew install miniconda
```

then:

```
conda create --name mlpipeline38 python=3.8
conda activate mlpipeline38
```

finally:

```
pip install kfp==2.7
```

Connect KFP pipelines [to client from outside cluster](https://www.kubeflow.org/docs/components/pipelines/v1/sdk/connect-api/#standalone-kubeflow-pipelines-subfrom-outside-clustersub):

```shell
kubectl port-forward --namespace kubeflow svc/ml-pipeline-ui 3000:80
```

Then you can use the client this way:

```python
import kfp

client = kfp.Client(host="http://localhost:3000")

print(client.list_experiments())
```

## Run the hello-world pipeline

Compile the pipeline to the kfp intermediate format:

```shell
python hello-world.py
```

Submit the pipeline:

```shell
python submit-hello.py 
```  

Check the pipeline with UI.

## Issues

The pipeline ui is does not show logs from S3/MinIO - see [this KFP issue](https://github.com/kubeflow/pipelines/issues/6428) 

I have created a fork with a fix for the UI in https://github.com/pdettori/pipelines/tree/s3-pod-logs-fix

The container image can be built with:

```shell
docker build -t quay.io/pdettori/kfp-frontend:2.0.5-fix -f frontend/Dockerfile .
```

The automation currently replaces ` gcr.io/ml-pipeline/frontend:2.0.5` => `quay.io/pdettori/kfp-frontend:2.0.5-fix`