# README

## Pre-reqs

Make sure you have the same pre-reqs required by [KubeStellar 0.22](https://docs.kubestellar.io/release-0.22.0/direct/pre-reqs/#kubestellar-prerequisites).

Make sure also go and make tools are installed. Note that this setup has been tested only on the
following environments:

- MacBook Pro:
  - Apple M1 Max, 64 GB Memory
  - MacOS 14.4.1
  - Rancher Desktop v1.13.1
  - kind v0.22.0
- IBM Cloud VM:
  - 64 GB RAM, 16 Cores
  - RHEL 9
  - Docker CE 26.1.1
  - kind v0.22.0


See how to install the pre-reqs for a RHEL 9 VM [here](./RHEL-KS-install.md).

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

For RHEL 9, follow [these instructions](./RHEL-KS-install.md#increasing-limits)


## Running the scripts

Note: at the start each script deletes yor current kubeflex, cluster1 and cluster2 clusters, and
backs up and delete your default kubeconfig in ~/.kube/config.

For each scenario (e.g. kfp), change directory `cd <scenario>` and run the `install-all` script:

```shell
./instal-all.sh
```
