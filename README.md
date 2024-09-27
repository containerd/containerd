![containerd banner light mode](https://raw.githubusercontent.com/cncf/artwork/master/projects/containerd/horizontal/color/containerd-horizontal-color.png#gh-light-mode-only)
![containerd banner dark mode](https://raw.githubusercontent.com/cncf/artwork/master/projects/containerd/horizontal/white/containerd-horizontal-white.png#gh-dark-mode-only)

[![PkgGoDev](https://pkg.go.dev/badge/github.com/containerd/containerd/v2)](https://pkg.go.dev/github.com/containerd/containerd/v2)
[![Build Status](https://github.com/containerd/containerd/actions/workflows/ci.yml/badge.svg?event=merge_group)](https://github.com/containerd/containerd/actions?query=workflow%3ACI+event%3Amerge_group)
[![Nightlies](https://github.com/containerd/containerd/workflows/Nightly/badge.svg)](https://github.com/containerd/containerd/actions?query=workflow%3ANightly)
[![Go Report Card](https://goreportcard.com/badge/github.com/containerd/containerd/v2)](https://goreportcard.com/report/github.com/containerd/containerd/v2)
[![CII Best Practices](https://bestpractices.coreinfrastructure.org/projects/1271/badge)](https://bestpractices.coreinfrastructure.org/projects/1271)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/containerd/containerd/badge)](https://scorecard.dev/viewer/?uri=github.com/containerd/containerd)
[![Check Links](https://github.com/containerd/containerd/actions/workflows/links.yml/badge.svg)](https://github.com/containerd/containerd/actions/workflows/links.yml)

containerd is an industry-standard container runtime with an emphasis on simplicity, robustness, and portability. It is available as a daemon for Linux and Windows, which can manage the complete container lifecycle of its host system: image transfer and storage, container execution and supervision, low-level storage and network attachments, etc.

containerd is a member of CNCF with ['graduated'](https://landscape.cncf.io/?selected=containerd) status.

containerd is designed to be embedded into a larger system, rather than being used directly by developers or end-users.

![architecture](docs/historical/design/architecture.png)

## Announcements

### Now Recruiting

We are a large inclusive OSS project that is welcoming help of any kind shape or form:
* Documentation help is needed to make the product easier to consume and extend.
* We need OSS community outreach/organizing help to get the word out; manage
and create messaging and educational content; and help with social media, community forums/groups, and google groups.
* We are actively inviting new [security advisors](https://github.com/containerd/project/blob/main/GOVERNANCE.md#security-advisors) to join the team.
* New subprojects are being created, core and non-core that could use additional development help.
* Each of the [containerd projects](https://github.com/containerd) has a list of issues currently being worked on or that need help resolving.
  - If the issue has not already been assigned to someone or has not made recent progress, and you are interested, please inquire.
  - If you are interested in starting with a smaller/beginner-level issue, look for issues with an `exp/beginner` tag, for example [containerd/containerd beginner issues.](https://github.com/containerd/containerd/issues?q=is%3Aissue+is%3Aopen+label%3Aexp%2Fbeginner)

## Getting Started

See our documentation on [containerd.io](https://containerd.io):
* [for ops and admins](docs/ops.md)
* [namespaces](docs/namespaces.md)
* [client options](docs/client-opts.md)

To get started contributing to containerd, see [CONTRIBUTING](CONTRIBUTING.md).

If you are interested in trying out containerd see our example at [Getting Started](docs/getting-started.md).

## Nightly builds

There are nightly builds available for download [here](https://github.com/containerd/containerd/actions?query=workflow%3ANightly).
Binaries are generated from `main` branch every night for `Linux` and `Windows`.

Please be aware: nightly builds might have critical bugs, it's not recommended for use in production and no support provided.

## Kubernetes (k8s) CI Dashboard Group

The [k8s CI dashboard group for containerd](https://testgrid.k8s.io/containerd) contains test results regarding
the health of kubernetes when run against main and a number of containerd release branches.

- [containerd-periodics](https://testgrid.k8s.io/containerd-periodic)

## Runtime Requirements

Runtime requirements for containerd are very minimal. Most interactions with
the Linux and Windows container feature sets are handled via [runc](https://github.com/opencontainers/runc) and/or
OS-specific libraries (e.g. [hcsshim](https://github.com/Microsoft/hcsshim) for Microsoft).
The current required version of `runc` is described in [RUNC.md](docs/RUNC.md).

There are specific features
used by containerd core code and snapshotters that will require a minimum kernel
version on Linux. With the understood caveat of distro kernel versioning, a
reasonable starting point for Linux is a minimum 4.x kernel version.

The overlay filesystem snapshotter, used by default, uses features that were
finalized in the 4.x kernel series. If you choose to use btrfs, there may
be more flexibility in kernel version (minimum recommended is 3.18), but will
require the btrfs kernel module and btrfs tools to be installed on your Linux
distribution.

To use Linux checkpoint and restore features, you will need `criu` installed on
your system. See more details in [Checkpoint and Restore](#checkpoint-and-restore).

Build requirements for developers are listed in [BUILDING](BUILDING.md).


## Supported Registries

Any registry which is compliant with the [OCI Distribution Specification](https://github.com/opencontainers/distribution-spec)
is supported by containerd.

For configuring registries, see [registry host configuration documentation](docs/hosts.md)

## Features

For a detailed overview of containerd's core concepts and the features it supports,
please refer to the [FEATURES.MD](./docs/features.md) document.

### Releases and API Stability

Please see [RELEASES.md](RELEASES.md) for details on versioning and stability
of containerd components.

Downloadable 64-bit Intel/AMD binaries of all official releases are available on
our [releases page](https://github.com/containerd/containerd/releases).

For other architectures and distribution support, you will find that many
Linux distributions package their own containerd and provide it across several
architectures, such as [Canonical's Ubuntu packaging](https://launchpad.net/ubuntu/bionic/+package/containerd).

#### Enabling command auto-completion

Starting with containerd 1.4, the urfave client feature for auto-creation of bash and zsh
autocompletion data is enabled. To use the autocomplete feature in a bash shell for example, source
the autocomplete/ctr file in your `.bashrc`, or manually like:

```
$ source ./contrib/autocomplete/ctr
```

#### Distribution of `ctr` autocomplete for bash and zsh

For bash, copy the `contrib/autocomplete/ctr` script into
`/etc/bash_completion.d/` and rename it to `ctr`. The `zsh_autocomplete`
file is also available and can be used similarly for zsh users.

Provide documentation to users to `source` this file into their shell if
you don't place the autocomplete file in a location where it is automatically
loaded for the user's shell environment.

### CRI

`cri` is a [containerd](https://containerd.io/) plugin implementation of the Kubernetes [container runtime interface (CRI)](https://github.com/kubernetes/cri-api/blob/master/pkg/apis/runtime/v1/api.proto). With it, you are able to use containerd as the container runtime for a Kubernetes cluster.

![cri](./docs/cri/cri.png)

#### CRI Status

`cri` is a native plugin of containerd. Since containerd 1.1, the cri plugin is built into the release binaries and enabled by default.

The `cri` plugin has reached GA status, representing that it is:
* Feature complete
* Works with Kubernetes 1.10 and above
* Passes all [CRI validation tests](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-node/cri-validation.md).
* Passes all [node e2e tests](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-node/e2e-node-tests.md).
* Passes all [e2e tests](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-testing/e2e-tests.md).

See results on the containerd k8s [test dashboard](https://testgrid.k8s.io/containerd)

#### Validating Your `cri` Setup
A Kubernetes incubator project, [cri-tools](https://github.com/kubernetes-sigs/cri-tools), includes programs for exercising CRI implementations. More importantly, cri-tools includes the program `critest` which is used for running [CRI Validation Testing](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-node/cri-validation.md).

#### CRI Guides
* [Installing with Ansible and Kubeadm](contrib/ansible/README.md)
* [For Non-Ansible Users, Preforming a Custom Installation Using the Release Tarball and Kubeadm](docs/getting-started.md)
* [CRI Plugin Testing Guide](./docs/cri/testing.md)
* [Debugging Pods, Containers, and Images with `crictl`](./docs/cri/crictl.md)
* [Configuring `cri` Plugins](./docs/cri/config.md)
* [Configuring containerd](https://github.com/containerd/containerd/blob/main/docs/man/containerd-config.8.md)

### Communication

For async communication and long-running discussions please use issues and pull requests on the GitHub repo.
This will be the best place to discuss design and implementation.

For sync communication catch us in the `#containerd` and `#containerd-dev` Slack channels on Cloud Native Computing Foundation's (CNCF) Slack - `cloud-native.slack.com`. Everyone is welcome to join and chat. [Get Invite to CNCF Slack.](https://slack.cncf.io)

### Security audit

Security audits for the containerd project are hosted on our website. Please see the [security page at containerd.io](https://containerd.io/security/) for more information.

### Reporting security issues

Please follow the instructions at [containerd/project](https://github.com/containerd/project/blob/main/SECURITY.md#reporting-a-vulnerability)

## Licenses

The containerd codebase is released under the [Apache 2.0 license](LICENSE).
The README.md file and files in the "docs" folder are licensed under the
Creative Commons Attribution 4.0 International License. You may obtain a
copy of the license, titled CC-BY-4.0, at http://creativecommons.org/licenses/by/4.0/.

## Project details

**containerd** is the primary open source project within the broader containerd GitHub organization.
However, all projects within the repo have common maintainership, governance, and contributing
guidelines which are stored in a `project` repository commonly for all containerd projects.

Please find all these core project documents, including the:
 * [Project governance](https://github.com/containerd/project/blob/main/GOVERNANCE.md),
 * [Maintainers](https://github.com/containerd/project/blob/main/MAINTAINERS),
 * and [Contributing guidelines](https://github.com/containerd/project/blob/main/CONTRIBUTING.md)

information in our [`containerd/project`](https://github.com/containerd/project) repository.

## Adoption

Interested to see who is using containerd? Are you using containerd in a project?
Please add yourself via pull request to our [ADOPTERS.md](./ADOPTERS.md) file.
