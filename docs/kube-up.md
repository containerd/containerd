# Production Quality Cluster on GCE
This document provides the steps to bring up a production quality cluster on GCE with [`kube-up.sh`](https://kubernetes.io/docs/setup/turnkey/gce/).

**If your Kubernetes version is 1.15 or greater, you can simply run:**
```
export KUBE_CONTAINER_RUNTIME=containerd
```
Follow these instructions [here](https://kubernetes.io/docs/setup/turnkey/gce/) to create a production quality Kubernetes cluster on GCE.
## Download CRI-Containerd Release Tarball
To download release tarball, see [step 1](./installation.md#step-1-download-cri-containerd-release-tarball) in installation.md.

Unpack release tarball to any directory, using `${CRI_CONTAINERD_PATH}` to indicate the directory in the doc:
```bash
tar -C ${CRI_CONTAINERD_PATH} -xzf cri-containerd-${VERSION}.linux-amd64.tar.gz
```
## Set Environment Variables for CRI-Containerd
```bash
. ${CRI_CONTAINERD_PATH}/opt/containerd/cluster/gce/env
```
## Create Kubernetes Cluster on GCE
Follow these instructions [here](https://kubernetes.io/docs/setup/turnkey/gce/) to create a production quality Kubernetes cluster on GCE.

**Make sure the Kubernetes version you are using is v1.11 or greater:**
* When using `https://get.k8s.io`, use the environment variable `KUBERNETES_RELEASE` to set version.
* When using a Kubernetes release tarball, make sure to select version 1.11 or greater.
