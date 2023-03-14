This documentation was merged into [`../getting-started.md`](../getting-started.md).
Please update your bookmark.


- - -
<!-- TODO: remove in containerd 2.0 -->
<details>
<summary>Show the original content (<strong>DEPRECATED</strong>)</summary>

<p>

# Install Containerd with Release Tarball
This document provides the steps to install `containerd` and its dependencies with the release tarball, and bring up a Kubernetes cluster using kubeadm.

These steps have been verified on Ubuntu 16.04. For other OS distributions, the steps may differ. Please feel free to file issues or PRs if you encounter any problems on other OS distributions.

*Note: You need to run the following steps on each node you are planning to use in your Kubernetes cluster.*

## Release Tarball
For each `containerd` release, we'll publish a release tarball specifically for Kubernetes named `cri-containerd-cni-${VERSION}-${OS}-${ARCH}.tar.gz`. This release tarball contains all required binaries and files for using `containerd` with Kubernetes. For example, the 1.4.3 version is available at https://github.com/containerd/containerd/releases/download/v1.4.3/cri-containerd-cni-1.4.3-linux-amd64.tar.gz.

### Content
As shown below, the release tarball contains:

- `containerd`, `containerd-shim-runc-v2`, `ctr`: binaries for containerd.
- `runc`: runc binary.
- `/opt/cni/bin`: binaries for [Container Network Interface](https://github.com/containernetworking/cni)
- `crictl`, `crictl.yaml`: command line tools for CRI container runtime and its config file.
- `critest`: binary to run [CRI validation test](https://github.com/kubernetes-sigs/cri-tools/blob/master/docs/validation.md).
- `containerd.service`: Systemd unit for containerd.
- `/opt/containerd/cluster/`: scripts for `kube-up.sh`.

```console
$ tar -tf cri-containerd-cni-1.4.3-linux-amd64.tar.gz
etc/
etc/cni/
etc/cni/net.d/
etc/cni/net.d/10-containerd-net.conflist
etc/crictl.yaml
etc/systemd/
etc/systemd/system/
etc/systemd/system/containerd.service
usr/
usr/local/
usr/local/bin/
usr/local/bin/containerd-shim-runc-v2
usr/local/bin/ctr
usr/local/bin/containerd-shim
usr/local/bin/containerd-shim-runc-v1
usr/local/bin/crictl
usr/local/bin/critest
usr/local/bin/containerd
usr/local/sbin/
usr/local/sbin/runc
opt/
opt/cni/
opt/cni/bin/
opt/cni/bin/vlan
opt/cni/bin/host-local
opt/cni/bin/flannel
opt/cni/bin/bridge
opt/cni/bin/host-device
opt/cni/bin/tuning
opt/cni/bin/firewall
opt/cni/bin/bandwidth
opt/cni/bin/ipvlan
opt/cni/bin/sbr
opt/cni/bin/dhcp
opt/cni/bin/portmap
opt/cni/bin/ptp
opt/cni/bin/static
opt/cni/bin/macvlan
opt/cni/bin/loopback
opt/containerd/
opt/containerd/cluster/
opt/containerd/cluster/version
opt/containerd/cluster/gce/
opt/containerd/cluster/gce/cni.template
opt/containerd/cluster/gce/configure.sh
opt/containerd/cluster/gce/cloud-init/
opt/containerd/cluster/gce/cloud-init/master.yaml
opt/containerd/cluster/gce/cloud-init/node.yaml
opt/containerd/cluster/gce/env
```

### Binary Information
Information about the binaries in the release tarball:

|           Binary Name          |      Support       |   OS  | Architecture |
|:------------------------------:|:------------------:|:-----:|:------------:|
|            containerd          | seccomp, apparmor, selinux<br/> overlay, btrfs | linux |     amd64    |
|          containerd-shim       |   overlay, btrfs   | linux |     amd64    |
|               runc             | seccomp, apparmor, selinux  | linux |     amd64    |


If you have other requirements for the binaries, e.g. another architecture support etc., you need to build the binaries yourself following [the instructions](../../BUILDING.md).

### Download

The release tarball could be downloaded from the release page https://github.com/containerd/containerd/releases.

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
wget https://github.com/containerd/containerd/releases/download/v${VERSION}/cri-containerd-cni-${VERSION}-linux-amd64.tar.gz
```
Validate checksum of the release tarball:
```bash
wget https://github.com/containerd/containerd/releases/download/v${VERSION}/cri-containerd-cni-${VERSION}-linux-amd64.tar.gz.sha256sum
sha256sum --check cri-containerd-cni-${VERSION}-linux-amd64.tar.gz.sha256sum
```
## Step 2: Install Containerd
If you are using systemd, just simply unpack the tarball to the root directory:
```bash
sudo tar --no-overwrite-dir -C / -xzf cri-containerd-cni-${VERSION}-linux-amd64.tar.gz
sudo systemctl daemon-reload
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

The next step is to use kubeadm to bring up the Kubernetes cluster. It is the same with [the ansible installer](../../contrib/ansible). Please follow the steps 2-4 [here](../../contrib/ansible/README.md#step-2).

</p>
</details>
