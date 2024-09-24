# README

These scripts are currently supporting deployment of KubeStellar, Kueue, and integration controllers on K3D Kubernetes only.

## Preparation

Before runnning the scripts, make sure to increase your ulimits and
fs.inotify.max_user_watches and fs.inotify.max_user_instances for the machine
running the kind clusters. See [these instructions](https://kind.sigs.k8s.io/docs/user/known-issues/#pod-errors-due-to-too-many-open-files) for more info.

For Rancher Desktop follow [these instructions](https://docs.rancherdesktop.io/how-to-guides/increasing-open-file-limit).
and use the following config:

```yaml
provision:
- mode: system
  script: |
    #!/bin/sh
    cat <<'EOF' > /etc/security/limits.d/rancher-desktop.conf
    * soft     nofile         82920
    * hard     nofile         82920
    EOF
    sysctl -w vm.max_map_count=262144
    sysctl -w fs.inotify.max_user_instances=8192
    sysctl -w fs.inotify.max_user_watches=1048576
```

Before running the script, make sure to run `go mod tidy`


## Running the scripts

Note: at the start each script deletes yor current kubeflex, cluster1 and cluster2 clusters, and
backs up and delete your default kubeconfig in ~/.kube/config.

To install, run the `install-all` script:

```shell
./instal-all.sh
```
