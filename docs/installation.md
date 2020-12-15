# Install Containerd with Release Tarball
This document provides the steps to install `containerd` and its dependencies with the release tarball, and bring up a Kubernetes cluster using kubeadm.

These steps have been verified on Ubuntu 16.04. For other OS distributions, the steps may differ. Please feel free to file issues or PRs if you encounter any problems on other OS distributions.

*Note: You need to run the following steps on each node you are planning to use in your Kubernetes cluster.*
## Release Tarball
For each `containerd` release, we'll publish a release tarball specifically for Kubernetes named `cri-containerd-${VERSION}.${OS}-${ARCH}.tar.gz`. This release tarball contains all required binaries and files for using `containerd` with Kubernetes. For example, the 1.2.4 version is available at https://storage.googleapis.com/cri-containerd-release/cri-containerd-1.2.4.linux-amd64.tar.gz.

Note: The VERSION tag specified for the tarball corresponds to the `containerd` release tag, not a containerd/cri repository release tag. The `containerd` release includes the containerd/cri repository code through vendoring. The containerd/cri version of the containerd/cri code included in `containerd` is specified via a commit hash for containerd/cri in containerd/containerd/vendor.conf.
### Content
As shown below, the release tarball contains:
1) `containerd`, `containerd-shim`, `containerd-stress`, `containerd-release`, `ctr`: binaries for containerd.
2) `runc`: runc binary.
3) `crictl`, `crictl.yaml`: command line tools for CRI container runtime and its config file.
4) `critest`: binary to run [CRI validation test](https://github.com/kubernetes-sigs/cri-tools/blob/master/docs/validation.md).
5) `containerd.service`: Systemd unit for containerd.
6) `/opt/containerd/cluster/`: scripts for `kube-up.sh`.
```console
$ tar -tf cri-containerd-1.1.0-rc.0.linux-amd64.tar.gz
./
./opt
./opt/containerd
./opt/containerd/cluster
./opt/containerd/cluster/gce
./opt/containerd/cluster/gce/cloud-init
./opt/containerd/cluster/gce/cloud-init/node.yaml
./opt/containerd/cluster/gce/cloud-init/master.yaml
./opt/containerd/cluster/gce/configure.sh
./opt/containerd/cluster/gce/env
./opt/containerd/cluster/version
./opt/containerd/cluster/health-monitor.sh
./usr
./usr/local
./usr/local/sbin
./usr/local/sbin/runc
./usr/local/bin
./usr/local/bin/crictl
./usr/local/bin/containerd
./usr/local/bin/containerd-stress
./usr/local/bin/critest
./usr/local/bin/containerd-release
./usr/local/bin/containerd-shim
./usr/local/bin/ctr
./etc
./etc/systemd
./etc/systemd/system
./etc/systemd/system/containerd.service
./etc/crictl.yaml
```
### Binary Information
Information about the binaries in the release tarball:

|           Binary Name          |      Support       |   OS  | Architecture |
|:------------------------------:|:------------------:|:-----:|:------------:|
|            containerd          | seccomp, apparmor, selinux<br/> overlay, btrfs | linux |     amd64    |
|          containerd-shim       |   overlay, btrfs   | linux |     amd64    |
|               runc             | seccomp, apparmor, selinux  | linux |     amd64    |


If you have other requirements for the binaries, e.g. another architecture support etc., you need to build the binaries yourself following [the instructions](../BUILDING.md).

### Download

The release tarball could be downloaded from the release GCS bucket https://storage.googleapis.com/cri-containerd-release/.

## Step 0: Install Dependent Libraries
Install required library for seccomp.
```bash
sudo apt-get update
sudo apt-get install libseccomp2
```
Note that:
1) If you are using Ubuntu <=Trusty or Debian <=jessie, a backported version of `libseccomp2` is needed. (See the [trusty-backports](https://packages.ubuntu.com/trusty-backports/libseccomp2) and [jessie-backports](https://packages.debian.org/jessie-backports/libseccomp2)).
## Step 1: Download Release Tarball
Download release tarball for the `containerd` version you want to install from the GCS bucket.
```bash
wget https://storage.googleapis.com/cri-containerd-release/cri-containerd-${VERSION}.linux-amd64.tar.gz
```
Validate checksum of the release tarball:
```bash
sha256sum cri-containerd-${VERSION}.linux-amd64.tar.gz
curl https://storage.googleapis.com/cri-containerd-release/cri-containerd-${VERSION}.linux-amd64.tar.gz.sha256
# Compare to make sure the 2 checksums are the same.
```
## Step 2: Install Containerd
If you are using systemd, just simply unpack the tarball to the root directory:
```bash
sudo tar --no-overwrite-dir -C / -xzf cri-containerd-${VERSION}.linux-amd64.tar.gz
sudo systemctl start containerd
```
If you are not using systemd, please unpack all binaries into a directory in your `PATH`, and start `containerd` as monitored long running services with the service manager you are using e.g. `supervisord`, `upstart` etc.
## Step 3: Install Kubeadm, Kubelet and Kubectl
Follow [the instructions](https://kubernetes.io/docs/setup/independent/install-kubeadm/) to install kubeadm, kubelet and kubectl.
## Step 4: Create Systemd Drop-In for Containerd
Create the systemd drop-in file `/etc/systemd/system/kubelet.service.d/0-containerd.conf`:
```
[Service]
Environment="KUBELET_EXTRA_ARGS=--container-runtime=remote --runtime-request-timeout=15m --container-runtime-endpoint=unix:///run/containerd/containerd.sock"
```
And reload systemd configuration:
```bash
systemctl daemon-reload
```
## Bring Up the Cluster
Now you should have properly installed all required binaries and dependencies on each of your node.

The next step is to use kubeadm to bring up the Kubernetes cluster. It is the same with [the ansible installer](../contrib/ansible). Please follow the steps 2-4 [here](../contrib/ansible/README.md#step-2).
