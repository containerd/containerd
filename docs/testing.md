CRI-Containerd Testing Guide
============================
This document assumes you have already setup the development environment (go, git, `cri-containerd` repo etc.).
 
Before sending pull requests you should at least make sure your changes have passed code verification, unit and CRI validation tests.
## Code Verification
Code verification includes lint, code formatting, boilerplate check etc.

* Install tools used by code verification:
```shell
make install.tools
```
* Run code verification:
```shell
make verify
```
## Unit Test
Run all unit tests in `cri-containerd` repo.
```
make test
```
## CRI Validation Test
[CRI validation test](https://github.com/kubernetes/community/blob/master/contributors/devel/cri-validation.md) is a test framework for validating that a Container Runtime Interface (CRI) implementation such as `cri-containerd` meets all the requirements necessary to manage pod sandboxes, containers, images etc.

CRI validation test makes it possible to verify CRI conformance of `cri-containerd` without setting up Kubernetes components or running Kubernetes end-to-end tests.
* [Install dependencies](../README.md#install-dependencies).
* Build `cri-containerd`:
```shell
make
```
* Run CRI validation test:
```shell
make test-cri
```
* Focus or skip specific CRI validation test:
```shell
make test-cri FOCUS=REGEXP_TO_FOCUS SKIP=REGEXP_TO_SKIP
```
[More information](https://github.com/kubernetes-incubator/cri-tools) about CRI validation test.
## Node E2E Test
[Node e2e test](https://github.com/kubernetes/community/blob/master/contributors/devel/e2e-node-tests.md) is a test framework testing Kubernetes node level functionalities such as managing pods, mounting volumes etc. It starts a local cluster with Kubelet and a few other minimum dependencies, and runs node functionality tests against the local cluster.

* Setup Kubernetes development environment following [Kubernetes Development Guide](https://github.com/kubernetes/community/blob/master/contributors/devel/development.md).
* [Install dependencies](../README.md#install-dependencies).
* Build and install `cri-containerd`:
```shell
make
sudo make install
```
* Start `containerd` in 1st terminal:
```shell
sudo containerd
``` 
* Start `cri-containerd` in 2nd terminal:
```shell
sudo cri-containerd -v 2 --alsologtostderr  
```
* Run node e2e test **from Kubernetes project directory** in 3rd terminal:
```shell
make test-e2e-node RUNTIME=remote CONTAINER_RUNTIME_ENDPOINT=/var/run/cri-containerd.sock 
```
*Note that `cri-containerd` is still in alpha, it can only pass part of node e2e tests now.*
<!-- TODO: Get rid of this after all node e2e tests pass. -->

[More information](https://github.com/kubernetes/community/blob/master/contributors/devel/e2e-node-tests.md) about Kubernetes node e2e test.
<!-- TODO: Add cluster e2e test -->
