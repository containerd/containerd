![cri-o logo](https://cdn.rawgit.com/kubernetes-incubator/cri-o/master/logo/crio-logo.svg)
# cri-o - OCI-based implementation of Kubernetes Container Runtime Interface

[![Build Status](https://img.shields.io/travis/kubernetes-incubator/cri-o.svg?maxAge=2592000&style=flat-square)](https://travis-ci.org/kubernetes-incubator/cri-o)
[![Go Report Card](https://goreportcard.com/badge/github.com/kubernetes-incubator/cri-o?style=flat-square)](https://goreportcard.com/report/github.com/kubernetes-incubator/cri-o)

### Status: alpha

## What is the scope of this project?

cri-o is meant to provide an integration path between OCI conformant runtimes and the kubelet.
Specifically, it implements the Kubelet Container Runtime Interface (CRI) using OCI conformant runtimes.
The scope of cri-o is tied to the scope of the CRI.

At a high level, we expect the scope of cri-o to be restricted to the following functionalities:

* Support multiple image formats including the existing Docker image format
* Support for multiple means to download images including trust & image verification
* Container image management (managing image layers, overlay filesystems, etc)
* Container process lifecycle management
* Monitoring and logging required to satisfy the CRI
* Resource isolation as required by the CRI

## What is not in scope for this project?

* Building, signing and pushing images to various image storages
* A CLI utility for interacting with cri-o. Any CLIs built as part of this project are only meant for testing this project and there will be no guarantees on the backwards compatibility with it.

This is an implementation of the Kubernetes Container Runtime Interface (CRI) that will allow Kubernetes to directly launch and manage Open Container Initiative (OCI) containers.

The plan is to use OCI projects and best of breed libraries for different aspects:
- Runtime: [runc](https://github.com/opencontainers/runc) (or any OCI runtime-spec implementation) and [oci runtime tools](https://github.com/opencontainers/runtime-tools)
- Images: Image management using [containers/image](https://github.com/containers/image)
- Storage: Storage and management of image layers using [containers/storage](https://github.com/containers/storage)
- Networking: Networking support through use of [CNI](https://github.com/containernetworking/cni)

It is currently in active development in the Kubernetes community through the [design proposal](https://github.com/kubernetes/kubernetes/pull/26788).  Questions and issues should be raised in the Kubernetes [sig-node Slack channel](https://kubernetes.slack.com/archives/sig-node).

## Commands
| Command                                              | Description                                                                                          |
| ---------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| [crio(8)](/docs/crio.8.md)                 | Enable OCI Kubernetes Container Runtime daemon |
| [kpod(1)](/docs/kpod.1.md)                 | Simple management tool for pods and images |
| [kpod-history(1)](/docs/kpod-history.1.md)] | Shows the history of an image |
| [kpod-images(1)](/docs/kpod-images.1.md)   | List images in local storage |
| [kpod-inspect(1)](/docs/kpod-inspect.1.md)       | Display the configuration of a container or image |
| [kpod-load(1)](/docs/kpod-load.1.md)       | Load an image from docker archive or oci |
| [kpod-pull(1)](/docs/kpod-pull.1.md)       | Pull an image from a registry |
| [kpod-push(1)](/docs/kpod-push.1.md)       | Push an image to a specified destination |
| [kpod-rmi(1)](/docs/kpod-rmi.1.md)         | Removes one or more images   |
| [kpod-save(1)](/docs/kpod-save.1.md)       | Saves an image to an archive |
| [kpod-tag(1)](/docs/kpod-tag.1.md)         | Add an additional name to a local image |
| [kpod-version(1)](/docs/kpod-version.1.md) | Display the Kpod Version Information |

## Configuration
| File                                       | Description                                                                                          |
| ---------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| [crio.conf(5)](/docs/crio.conf.5.md)       | CRI-O Configuation file |

## Communication

For async communication and long running discussions please use issues and pull requests on the github repo. This will be the best place to discuss design and implementation.

For sync communication we have an IRC channel #cri-o, on chat.freenode.net, that everyone is welcome to join and chat about development.

## Getting started

### Prerequisites

Latest verion of `runc` is expected to be installed on the system. It is picked up as the default runtime by crio.

### Build Dependencies

**Required**

Fedora, CentOS, RHEL, and related distributions:

```bash
yum install -y \
  btrfs-progs-devel \
  device-mapper-devel \
  glib2-devel \
  glibc-devel \
  glibc-static \
  gpgme-devel \
  libassuan-devel \
  libgpg-error-devel \
  libseccomp-devel \
  libselinux-devel \
  ostree-devel \
  pkgconfig \
  runc
```

Debian, Ubuntu, and related distributions:

```bash
apt install -y \
  btrfs-tools \
  libassuan-dev \
  libdevmapper-dev \
  libglib2.0-dev \
  libc6-dev \
  libgpgme11-dev \
  libgpg-error-dev \
  libseccomp-dev \
  libselinux1-dev \
  pkg-config \
  runc
```

Debian, Ubuntu, and related distributions will also need a copy of the development libraries for `ostree`, either in the form of the `libostree-dev` package from the [flatpak](https://launchpad.net/~alexlarsson/+archive/ubuntu/flatpak) PPA, or built [from source](https://github.com/ostreedev/ostree) (more on that [here](https://ostree.readthedocs.io/en/latest/#building)).

If using an older release or a long-term support release, be careful to double-check that the version of `runc` is new enough (running `runc --version` should produce `spec: 1.0.0`), or else build your own.

**Optional**

Fedora, CentOS, RHEL, and related distributions:

(no optional packages)

Debian, Ubuntu, and related distributions:

```bash
apt install -y \
  libapparmor-dev
```

### Get Source Code

As with other Go projects, cri-o must be cloned into a directory structure like:

```
GOPATH
└── src
    └── github.com
        └── kubernetes-incubator
            └── cri-o
```

First, configure a `GOPATH` (if you are using go1.8 or later, this defaults to `~/go`).

```bash
export GOPATH=~/go
mkdir -p $GOPATH
```

Next, clone the source code using:

```bash
mkdir -p $GOPATH/src/github.com/kubernetes-incubator
cd $_ # or cd $GOPATH/src/github.com/kubernetes-incubator
git clone https://github.com/kubernetes-incubator/cri-o # or your fork
cd cri-o
```

### Build

```bash
make install.tools
make
sudo make install
```

Otherwise, if you do not want to build `cri-o` with seccomp support you can add `BUILDTAGS=""` when running make.

```bash
make BUILDTAGS=""
sudo make install
```

#### Build Tags

`cri-o` supports optional build tags for compiling support of various features.
To add build tags to the make option the `BUILDTAGS` variable must be set.

```bash
make BUILDTAGS='seccomp apparmor'
```

| Build Tag | Feature                            | Dependency  |
|-----------|------------------------------------|-------------|
| seccomp   | syscall filtering                  | libseccomp  |
| selinux   | selinux process and mount labeling | libselinux  |
| apparmor  | apparmor profile support           | libapparmor |

### Running pods and containers

Follow this [tutorial](tutorial.md) to get started with CRI-O.

### Setup CNI networking

A proper description of setting up CNI networking is given in the
[`contrib/cni` README](contrib/cni/README.md). But the gist is that you need to
have some basic network configurations enabled and CNI plugins installed on
your system.

### Running with kubernetes

You can run a local version of kubernetes with cri-o using `local-up-cluster.sh`:

1. Clone the [kubernetes repository](https://github.com/kubernetes/kubernetes)
1. Start the cri-o daemon (`crio`)
1. From the kubernetes project directory, run: `CONTAINER_RUNTIME=remote CONTAINER_RUNTIME_ENDPOINT='/var/run/crio.sock --runtime-request-timeout=15m' ./hack/local-up-cluster.sh`

To run a full cluster, see [the instructions](kubernetes.md).

### Current Roadmap

1. Basic pod/container lifecycle, basic image pull (done)
1. Support for tty handling and state management (done)
1. Basic integration with kubelet once client side changes are ready (done)
1. Support for log management, networking integration using CNI, pluggable image/storage management (done)
1. Support for exec/attach (done)
1. Target fully automated kubernetes testing without failures [e2e status](https://github.com/kubernetes-incubator/cri-o/issues/533)
1. Release 1.0
1. Track upstream k8s releases
