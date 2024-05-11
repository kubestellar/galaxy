# README

## Pre-reqs

Make sure you have the same pre-reqs required by [KubeStellar 0.22](https://docs.kubestellar.io/release-0.22.0/direct/pre-reqs/#kubestellar-prerequisites). 
Make sure also go and make tools are installed. As for kind and docker,
this setup has been tested with kind v0.22.0 and Rancher Desktop v1.13.1 on macOS apple silicon.
It has also been tested on a RHEL 9 VM with 64 GB RAM and 16 Cores on [Draco](https://ocp-draco.bx.cloud9.ibm.com).
See how to setup the VM [here](docs/RHEL-KS-install.md).

## Preparation

Before runnning the scripts, make sure to increase your ulimits and 
fs.inotify.max_user_watches and fs.inotify.max_user_instances. 

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

For RHEL 9, follow [these instructions](docs/RHEL-KS-install.md#increasing-limits)


## Running the script

Note: at the start the script deletes yor current kubeflex, cluster1 and cluster2 clusters, and
backs up and delete your default kubeconfig in ~/.kube/config.

Run the script:

```shell
kfp/instal-all.sh
```