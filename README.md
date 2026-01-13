# galaxy

Additional modules, tools and documentation to facilitate KubeStellar integration with other community projects.

This project includes bash-based scripts to replicate demos and PoCs such as KFP + KubeStellar integration, Argo Workflows + KubeStellar, and Kueue + KubeStellar integrations.

- [suspend-webook](./suspend-webhook/) webook used to suspend argo workflows (and in the future other types of
workloads supporting the suspend flag)

- [shadow-pods](./shadow-pods/) controller used to support streaming logs in Argo Workflows and KFP.

- [clustermetrics](./clustermetrics/) - a CRD and controller that provide
basic cluster metrics info for each node in a cluster, designed to work together with KubeStellar
sync/status sycn mechanisms.

- [mc-scheduling](./mc-scheduling/) -A Multi-cluster scheduling framework supporting pluggable schedulers.

- [kueue-ks](./kueue-ks/) -Set of controllers enabling integration of Kueue with KubeStellar.

## KubeFlow Pipelines v2

Check out this [instructions](./scripts/kfp/)


## Argo Workflows

Check out this [instructions](./scripts/argo-wf/)

## kueue-ks
Check out this [instructions](./scripts/kueue/)

<!-- CI Test: 2026-01-13T03:00:18Z -->
