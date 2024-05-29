# Multi-cluster Job Workload Management with Kueue and KubeStellar
This project aims to simplify the deployment and management of batch workloads across multiple Kubernetes clusters using [Kueue](https://kueue.sigs.k8s.io) for job queueing and [KubeStellar](https://docs.kubestellar.io) for multi-cluster configuration management. 


## Overview
This repository contains two core controllers:

- *WorkloadController:* watches for Kueue `Workload` objects and orchestrates the downsync and deployment of corresponding jobs to worker clusters managed by KubeStellar
- *QuotaManagerController:* monitors [ClusterMetrics](https://github.com/kubestellar/galaxy/tree/main/clustermetrics) from each worker cluster and dynamically updates Kueue's global resource quotas as needed

## Description
In multi-cluster Kubernetes environments, managing batch workloads and ensuring efficient resource utilization across clusters can be a complex challenge. Organizations often face issues such as resource contention, over-provisioning, and inefficient workload distribution, leading to suboptimal resource utilization and increased costs.

The kueue-ks project goal is to address these challenges by leveraging Kueue's quota management capabilities and integrating with KubeStellar for multi-cluster configuration management. The primary goal is to enable centralized management and intelligent distribution of batch workloads across multiple clusters based on available resource quotas. 


## Getting Started
You’ll need a Kubernetes cluster to run against. You can use [K3D](https://k3d.io) to get a local cluster for testing.

### Running on the K3D cluster
1. Check out this [instructions](./scripts/kueue/)

2. Run job examples:

```sh
kubectl create -f examples/batch-job.yaml
```

```sh
kubectl create -f examples/pytorch-simple-job.yaml
```

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

### How it works
This project aims to follow the Kubernetes [Operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)

It uses [Controllers](https://kubernetes.io/docs/concepts/architecture/controller/) 
which provides a reconcile function responsible for synchronizing resources untile the desired state is reached on the cluster 

### Test It Out
1. Install the CRDs into the cluster:

```sh
make install
```

2. Run your controller (this will run in the foreground, so switch to a new terminal if you want to leave it running):

```sh
make run
```

**NOTE:** You can also run this in one step by running: `make install run`

### Modifying the API definitions
If you are editing the API definitions, generate the manifests such as CRs or CRDs using:

```sh
make manifests
```

**NOTE:** Run `make --help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2024 The KubeStellar Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

