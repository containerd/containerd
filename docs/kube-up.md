# Production Quality Cluster on GCE
This document provides the steps to bring up a production quality cluster on GCE with [`kube-up.sh`](https://kubernetes.io/docs/getting-started-guides/gce/).

## Download CRI-Containerd Release Tarball
To download release tarball, see [step 1](./installation.md#step-1-download-cri-containerd-release-tarball) in installation.md.

Unpack release tarball to any directory, using `${CRI_CONTAINERD_PATH}` to indicate the directory in the doc:
```bash
tar -C ${CRI_CONTAINERD_PATH} -xzf cri-containerd-${VERSION}.linux-amd64.tar.gz
```
## Set Environment Variables for CRI-Containerd
Use environment variable `CRI_CONTAINERD_VERSION` to specify `cri-containerd` version. By default,
latest version will be used.
```bash
. ${CRI_CONTAINERD_PATH}/cluster/gce/env
```
## Create Kubernetes Cluster on GCE
Follow these instructions [here](https://kubernetes.io/docs/getting-started-guides/gce/) to create a production quality Kubernetes cluster on GCE.

**Make sure the Kubernetes version you are using is v1.9 or greater:**
* When using `https://get.k8s.io`, use the environment variable `KUBERNETES_RELEASE` to set version.
* When using a Kubernetes release tarball, make sure to select version 1.9 or greater.
