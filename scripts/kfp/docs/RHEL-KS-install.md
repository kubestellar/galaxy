# Prereqs Installation instructions for KS on RHEL 9

## Installing prereqs

### Account setup

```shell
useradd <user>
passwd <user>
usermod -aG wheel <user>
visudo 
# add "<user> ALL=(ALL) NOPASSWD: ALL" 
```
### Installing docker

```shell
sudo yum-config-manager --add-repo https://download.docker.com/linux/rhel/docker-ce.repo
sudo yum install docker-ce
sudo systemctl start docker
sudo systemctl enable docker
sudo usermod -aG docker $USER
```

## Installing kubectl

```shell
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
sudo install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl
sudo yum install bash-completion
echo 'source /usr/share/bash-completion/bash_completion' >>~/.bashrc
echo 'alias k=kubectl' >>~/.bashrc
echo 'complete -o default -F __start_kubectl k' >>~/.bashrc
```

## Installing go and make

```shell
curl -LO https://go.dev/dl/go1.22.2.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.22.2.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >>~/.bashrc
yum install make git
```
## Installing other tools

```shell
sudo dnf install https://dl.fedoraproject.org/pub/epel/epel-release-latest-9.noarch.rpm
sudo dnf upgrade
sudo yum install snapd
sudo systemctl enable --now snapd.socket
sudo ln -s /var/lib/snapd/snap /snap
# logout and back before using snap
sudo snap install yq
```

## Installing kind from source

```shell
go install sigs.k8s.io/kind@v0.22.0
echo 'export PATH=$PATH:/home/paolo/go/bin' >>~/.bashrc
```

## Installing helm

```shell
curl -LO https://get.helm.sh/helm-v3.14.4-linux-amd64.tar.gz
tar xzvf helm-v3.14.4-linux-amd64.tar.gz 
sudo install -o root -g root -m 0755 linux-amd64/helm /usr/local/bin/helm
```

## Installing ko

```shell
go install github.com/google/ko@latest
```

## Increasing limits

```shell
 sudo sysctl -w fs.inotify.max_user_instances=8192
 sudo sysctl -w fs.inotify.max_user_watches=1048576
 sudo vim /etc/security/limits.conf
 ```

Add the following in `/etc/security/limits.conf`

```
*               soft    nofile          16384
*               hard    nofile          16384
```

To make the change persistent:

edit the following file:

sudo vim `/etc/sysctl.d/inotify.conf`

and add:

```
fs.inotify.max_user_watches=65536
fs.inotify.max_user_instances=8192
```

Check the result with `sysctl fs.inotify`

## Installing pip

```shell
wget https://bootstrap.pypa.io/get-pip.py
python3 ./get-pip.py
```

## Installing kfp sdk

```shell
python3 -m pip install kfp kfp-server-api --upgrade
```