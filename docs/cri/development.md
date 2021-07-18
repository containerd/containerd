# Getting Started for Developers

This document provides steps to build `cri` plugin and test it locally.

> NOTE: This is part of the migration from: https://github.com/containerd/cri#getting-started-for-developers

## Install Dependencies

`sudo make install-deps`

## Build and Install cri

When building the containerd, cri is automatically being built unless you specify no_cri build tag. For more detail, see this [guide](https://github.com/containerd/containerd/blob/master/BUILDING.md#build-containerd)

### Running a Kubernetes local cluster

If you already have a working development environment for supported Kubernetes
version, you can try `cri` in a local cluster:

0. If containerd is already running as a daemon service, you need to stop it first.
```bash
sudo systemctl stop containerd
```

1. Start the version of `containerd` with `cri` plugin that you built and installed
above as root in a first terminal:
```bash
sudo containerd -l debug
```

After containerd is up, you can verify it with [crictl](https://github.com/kubernetes-sigs/cri-tools/blob/master/docs/crictl.md) tool:
```bash
sudo crictl info
```
You will see the containerd runtime information as output.

2. From the Kubernetes project directory startup a local cluster using `containerd`:
```bash
sudo CONTAINER_RUNTIME=remote CONTAINER_RUNTIME_ENDPOINT='unix:///run/containerd/containerd.sock' PATH=$PATH ./hack/local-up-cluster.sh
```

For more detail, refer to [the guide of running locally in Kubernetes](https://github.com/kubernetes/community/blob/master/contributors/devel/running-locally.md).

### Test

See [here](./testing.md) for information about test.