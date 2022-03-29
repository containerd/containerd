CRI Plugin Testing Guide
========================
This document assumes you have already setup the development environment (go, git, `github.com/containerd/containerd` repo etc.).

Before sending pull requests you should at least make sure your changes have passed code verification, unit, integration and CRI validation tests.

## Build
Follow the [building](../../BUILDING.md) instructions.

## CRI Integration Test
* Run all CRI integration tests:
```bash
make cri-integration
```
* Run specific CRI integration tests: use the `FOCUS` parameter to specify the test case.
```bash
# run CRI integration tests that match the test string <TEST_NAME>
FOCUS=<TEST_NAME> make cri-integration
```
Example:
```bash
FOCUS=TestContainerListStats make cri-integration
```
## CRI Validation Test
[CRI validation test](https://github.com/kubernetes-sigs/cri-tools/blob/master/docs/validation.md) is a test framework for validating that a Container Runtime Interface (CRI) implementation such as containerd with the `cri` plugin meets all the requirements necessary to manage pod sandboxes, containers, images etc.

CRI validation test makes it possible to verify CRI conformance of `containerd` without setting up Kubernetes components or running Kubernetes end-to-end tests.
* [Install dependencies](https://github.com/kubernetes-sigs/cri-tools/blob/master/docs/validation.md#install).
* Run the containerd you built above with the `cri` plugin built in:
```bash
containerd -l debug
```
* Run CRI [validation](https://github.com/kubernetes-sigs/cri-tools/blob/master/docs/validation.md#run) test.

[More information](https://github.com/kubernetes-sigs/cri-tools) about CRI validation test.
## Node E2E Test
[Node e2e test](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-node/e2e-node-tests.md) is a test framework testing Kubernetes node level functionalities such as managing pods, mounting volumes etc. It starts a local cluster with Kubelet and a few other minimum dependencies, and runs node functionality tests against the local cluster.

Currently e2e-node tests are supported from via Pull Request comments on github.
Enter "/test all" as a comment on a pull request for a list of testing options that have been integrated through prow bot with kubernetes testing services hosted on GCE.
Typing `/test pull-containerd-node-e2e` will start a node e2e test run on your pull request commits.

[More information](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-node/e2e-node-tests.md) about Kubernetes node e2e test.
