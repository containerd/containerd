# cri-containerd
<p align="center">
<img src="https://github.com/kubernetes/kubernetes/blob/master/logo/logo.png" width="50" height="50">
<img src="https://github.com/containerd/containerd/blob/master/docs/images/containerd-dark.png" width="200" >
</p>

[![Build Status](https://api.travis-ci.org/kubernetes-incubator/cri-containerd.svg?style=flat-square)](https://travis-ci.org/kubernetes-incubator/cri-containerd)
[![Go Report Card](https://goreportcard.com/badge/github.com/kubernetes-incubator/cri-containerd?style=flat-square)](https://goreportcard.com/report/github.com/kubernetes-incubator/cri-containerd)

`cri-containerd` is a [containerd](https://containerd.io/) based implementation of Kubernetes [container runtime interface (CRI)](https://github.com/kubernetes/kubernetes/blob/v1.6.0/pkg/kubelet/api/v1alpha1/runtime/api.proto).
![cri-containerd](./docs/cri-containerd.png)
## Current Status
`cri-containerd` is in alpha. This release is for use with Kubernetes 1.8. See
the [roadmap](./docs/proposal.md#roadmap-and-milestones)
for information about current and future milestones.
## Installing with Ansible and Kubeadm
For a multi node cluster installer and bring up steps using ansible and kubeadm refer [here](contrib/ansible/README.md).
## Getting Started for Developers
### Binary Dependencies and Specifications
The current release of `cri-containerd` has following depedencies:
* [containerd](https://github.com/containerd/containerd)
* [runc](https://github.com/opencontainers/runc)
* [CNI](https://github.com/containernetworking/cni)

See [versions](./hack/versions) of these dependencies `cri-containerd` is tested with.

As containerd and runc move to their respective general availability releases,
we will do our best to rebase/retest `cri-containerd` with these releases on a
weekly/monthly basis. Similarly, given that `cri-containerd` uses the Open
Container Initiative (OCI) [image](https://github.com/opencontainers/image-spec)
and [runtime](https://github.com/opencontainers/runtime-spec) specifications, we
will also do our best to update `cri-containerd` to the latest releases of these
specifications as appropriate.
### Install Dependencies
1. Install runc dependencies.
* runc requires installation of the libsecomp development library appropriate
for your distribution. `libseccomp-dev` (Ubuntu, Debian) / `libseccomp-devel`
(Fedora, CentOS, RHEL). On releases of Ubuntu <=Trusty and Debian <=jessie a
backport version of `libsecomp-dev` is required. See [travis.yml](.travis.yml)
for an example on trusty. To use apparmor on Debian, Ubuntu, and related
distributions runc requires the installation of `libapparmor-dev`.
2. Install containerd dependencies.
* containerd requires installation of a btrfs development library. `btrfs-tools`(Ubuntu, Debian) / `btrfs-progs-devel`(Fedora, CentOS, RHEL)
3. Install other dependencies:
* `nsenter`: Required by CNI and portforward.
* `socat`: Required by portforward.
4. Install and setup a go1.8.x development environment.
5. Make a local clone of this repository.
6. Install binary dependencies by running the following command from your cloned `cri-containerd/` project directory:
```shell
# Note: install.deps installs the above mentioned runc, containerd, and CNI
# binary dependencies. install.deps is only provided for general use and ease of
# testing. To customize `runc` and `containerd` build tags and/or to configure
# `cni`, please follow instructions in their documents.
make install.deps
```
### Build and Install cri-containerd
To build and install `cri-containerd` enter the following commands from your `cri-containerd` project directory:
```shell
make
sudo make install
```
### Validate Your cri-containerd Setup
Another Kubernetes incubator project called [cri-tools](https://github.com/kubernetes-incubator/cri-tools)
includes programs for exercising CRI implementations such as `cri-containerd`.
More importantly, cri-tools includes the program `critest` which is used for running
[CRI Validation Testing](https://github.com/kubernetes/community/blob/master/contributors/devel/cri-validation.md).

Run the CRI Validation test to validate your installation of `cri-containerd`:
```shell
make test-cri
```
### Running with Kubernetes
If you already have a working development environment for Kubernetes, you can
try `cri-containerd` in a local cluster:

1. Start `containerd` as root in a first terminal:
```shell
sudo containerd
```
2. Start `cri-containerd` as root in a second terminal:
```shell
sudo cri-containerd -v 2 --alsologtostderr
```
3. From the kubernetes project directory startup a local cluster using `cri-containerd`:
```shell
CONTAINER_RUNTIME=remote CONTAINER_RUNTIME_ENDPOINT='/var/run/cri-containerd.sock' ./hack/local-up-cluster.sh
```
## Documentation
See [here](./docs) for additional documentation.
## Contributing
Interested in contributing? Check out the [documentation](./CONTRIBUTING.md).

## Kubernetes Incubator
This is a [Kubernetes Incubator project](https://github.com/kubernetes/community/blob/master/incubator.md).
The project was established 2017/4/13. The incubator team for the project is:
* Sponsor: Dawn Chen ([@dchen1107](https://github.com/dchen1107))
* Champion: Yuju Hong ([@yujuhong](https://github.com/yujuhong))
* SIG: `sig-node`

For more information about `sig-node` and the `cri-containerd` project:
* [sig-node community site](https://github.com/kubernetes/community/tree/master/sig-node)
* Slack: `#sig-node` channel in Kubernetes (kubernetes.slack.com)
* Mailing List: https://groups.google.com/forum/#!forum/kubernetes-sig-node
## Code of Conduct
Participation in the Kubernetes community is governed by the
[Kubernetes Code of Conduct](./code-of-conduct.md).
