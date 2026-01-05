# Node Resource Interface, Revisited

[![PkgGoDev](https://pkg.go.dev/badge/github.com/containerd/nri)](https://pkg.go.dev/github.com/containerd/nri)
[![Build Status](https://github.com/containerd/nri/workflows/CI/badge.svg)](https://github.com/containerd/nri/actions?query=workflow%3ACI)
[![codecov](https://codecov.io/gh/containerd/nri/branch/main/graph/badge.svg)](https://codecov.io/gh/containerd/nri)
[![Go Report Card](https://goreportcard.com/badge/github.com/containerd/nri)](https://goreportcard.com/report/github.com/containerd/nri)

*This project is currently in DRAFT status*

## Goal

NRI allows plugging domain- or vendor-specific custom logic into OCI-
compatible runtimes. This logic can make controlled changes to containers
or perform extra actions outside the scope of OCI at certain points in a
containers lifecycle. This can be used, for instance, for improved allocation
and management of devices and other container resources.

NRI defines the interfaces and implements the common infrastructure for
enabling such pluggable runtime extensions, NRI plugins. This also keeps
the plugins themselves runtime-agnostic.

The goal is to enable NRI support in the most commonly used OCI runtimes,
[containerd](https://github.com/containerd/containerd) and
[CRI-O](https://github.com/cri-o/cri-o).

## Background

The revisited API is a major rewrite of NRI. It changes the scope of NRI
and how it gets integrated into runtimes. It reworks how plugins are
implemented, how they communicate with the runtime, and what kind of
changes they can make to containers.

[NRI v0.1.0](README-v0.1.0.md) used an OCI hook-like one-shot plugin invocation
mechanism where a separate instance of a plugin was spawned for every NRI
event. This instance then used its standard input and output to receive a
request and provide a response, both as JSON data.

Plugins in NRI are daemon-like entities. A single instance of a plugin is
now responsible for handling the full stream of NRI events and requests. A
unix-domain socket is used as the transport for communication. Instead of
JSON requests and responses NRI is defined as a formal, protobuf-based
'NRI plugin protocol' which is compiled into ttRPC bindings. This should
result in improved communication efficiency with lower per-message overhead,
and enable straightforward implementation of stateful NRI plugins.

## Components

The NRI implementation consists of a number of components. The core of
these are essential for implementing working end-to-end NRI support in
runtimes. These core components are the actual [NRI protocol](pkg/api),
and the [NRI runtime adaptation](pkg/adaptation).

Together these establish the model of how a runtime interacts with NRI and
how plugins interact with containers in the runtime through NRI. They also
define under which conditions plugins can make changes to containers and
the extent of these changes.

The rest of the components are the [NRI plugin stub library](pkg/stub)
and [some sample NRI plugins](plugins). [Some plugins](plugins/hook-injector)
implement useful functionality in real world scenarios. [A few](plugins/differ)
[others](plugins/logger) are useful for debugging. All of the sample plugins
serve as practical examples of how the stub library can be used to implement
NRI plugins.

## Protocol, Plugin API

The core of NRI is defined by a protobuf [protocol definition](pkg/api/api.proto)
of the low-level plugin API. The API defines two services, Runtime and Plugin.

The Runtime service is the public interface runtimes expose for NRI plugins. All
requests on this interface are initiated by the plugin. The interface provides
functions for

  - initiating plugin registration
  - requesting unsolicited updates to containers

The Plugin service is the public interface NRI uses to interact with plugins.
All requests on this interface are initiated by NRI/the runtime. The interface
provides functions for

  - configuring the plugin
  - getting initial list of already existing pods and containers
  - hooking the plugin into pod/container lifecycle events
  - shutting down the plugin

An additional interface is provided for validating the changes active plugins
have requested to containers. This interface allows one to set up and enforce
cluster- or node-wide boundary conditions for changes NRI plugins are allowed
to make.

### Plugin Registration

Before a plugin can start receiving and processing container events, it needs
to register itself with NRI. During registration the plugin and NRI perform a
handshake sequence which consists of the following steps:

  1. the plugin identifies itself to the runtime
  2. NRI provides plugin-specific configuration data to the plugin
  3. the plugin subscribes to pod and container lifecycle events of interest
  4. NRI sends list of existing pods and containers to plugin
  5. the plugin requests any updates deemed necessary to existing containers

The plugin identifies itself to NRI by a plugin name and a plugin index. The
plugin index is used by NRI to determine in which order the plugin is hooked
into pod and container lifecycle event processing with respect to any other
plugins.

The plugin name is used to pick plugin-specific data to send to the plugin
as configuration. This data is only present if the plugin has been launched
by NRI. If the plugin has been externally started it is expected to acquire
its configuration also by external means. The plugin subscribes to pod and
container lifecycle events of interest in its response to configuration.

As the last step in the registration and handshaking process, NRI sends the
full set of pods and containers known to the runtime. The plugin can request
updates it considers necessary to any of the known containers in response.

Once the handshake sequence is over and the plugin has registered with NRI,
it will start receiving pod and container lifecycle events according to its
subscription.

### Pod Data and Available Lifecycle Events

<details>
<summary>NRI Pod Lifecycle Events</summary>
<p align="center">
<img src="./docs/nri-pod-lifecycle.svg" title="NRI Pod Lifecycle Events">
</p>
</details>

NRI plugins can subscribe to the following pod lifecycle events:

  - creation
  - stopping
  - removal

The following pieces of pod metadata are available to plugins in NRI:

  - ID
  - name
  - UID
  - namespace
  - labels
  - annotations
  - cgroup parent directory
  - runtime handler name

### Container Data and Available Lifecycle Events

<details>
<summary>NRI Container Lifecycle Events</summary>
<p align="center">
<img src="./docs/nri-container-lifecycle.svg" title="NRI Container Lifecycle Events">
</p>
</details>

NRI plugins can subscribe to the following container lifecycle events:

  - creation (*)
  - post-creation
  - starting
  - post-start
  - updating (*)
  - post-update
  - stopping (*)
  - removal

*) Plugins can request adjustment or updates to containers in response to
these events.

The following pieces of container metadata are available to plugins in NRI:

  - ID
  - pod ID
  - name
  - state
  - labels
  - annotations
  - command line arguments
  - environment variables
  - mounts
  - user, group and supplemental group IDs
  - OCI hooks
  - rlimits
  - I/O priority
  - linux
    - namespace IDs
    - devices
    - resources
      - memory
        - limit
        - reservation
        - swap limit
        - kernel limit
        - kernel TCP limit
        - swappiness
        - OOM disabled flag
        - hierarchical accounting flag
        - hugepage limits
      - CPU
        - shares
        - quota
        - period
        - realtime runtime
        - realtime period
        - cpuset CPUs
        - cpuset memory
      - Block I/O class
      - RDT class
      - Unified cgroup v2 parameter map
    - Linux seccomp profile and policy
    - Linux network devices
    - scheduling policy parameters
  - container (init) process ID
  - container (init process) exit status
  - timestamp of container creation
  - timestamp of starting the container
  - timestamp of stopping the container/container exit
  - container exit status reason (camelCase)
  - container exit status message (human readable)

Apart from data identifying the container, these pieces of information
represent the corresponding data in the container's [OCI Spec](https://github.com/opencontainers/runtime-spec/blob/main/spec.md).

### Container Adjustment

During container creation plugins can request changes to the following
container parameters:

  - annotations
  - mounts
  - command line arguments
  - environment variables
  - OCI hooks
  - rlimits
  - I/O priority
  - linux
    - devices
    - resources
      - memory
        - limit
        - reservation
        - swap limit
        - kernel limit
        - kernel TCP limit
        - swappiness
        - OOM disabled flag
        - hierarchical accounting flag
        - hugepage limits
      - CPU
        - shares
        - quota
        - period
        - realtime runtime
        - realtime period
        - cpuset CPUs
        - cpuset memory
      - Block I/O class
      - RDT class
      - Unified cgroup v2 parameter map
      - Linux seccomp policy
      - Linux network devices
    - Linux namespaces
    - scheduling policy parameters

### Container Updates

Once a container has been created plugins can request updates to them.
These updates can be requested in response to another containers creation
request, in response to any containers update request, in response to any
containers stop request, or they can be requested as part of a separate
unsolicited container update request. The following container parameters
can be updated this way:

  - resources
    - memory
      - limit
      - reservation
      - swap limit
      - kernel limit
      - kernel TCP limit
      - swappiness
      - OOM disabled flag
      - hierarchical accounting flag
      - hugepage limits
    - CPU
      - shares
      - quota
      - period
      - realtime runtime
      - realtime period
      - cpuset CPUs
      - cpuset memory
    - Block I/O class
    - RDT class
    - Unified cgroup v2 parameter map

### Container Adjustment Validation

NRI plugins operate as trusted extensions of the container runtime, granting
them significant privileges to alter container specs. While this extensibility
is powerful with valid use cases, some of the capabilities granted to plugins
allow modifying security-sensitive settings of containers. As such they also
come with the risk that a plugin could inadvertently or maliciously weaken a
container's isolation or security posture, potentially overriding policies set
by cluster orchestrators such as K8s.

NRI offers cluster administrators a mechanism to exercise fine-grained control
over what changes plugins are allowed to make to containers, allowing cluster
administrators to lock down selected features in NRI or allowing them to only
be used a subset of plugins. Changes in NRI are made in two phases: “Mutating”
plugins propose changes, and “Validating” plugins approve or deny them.

Validating plugins are invoked during container creation after all the changes
requested to containers have been collected. Validating plugins receive the
changes with extra information about which of the plugins requested what
changes. They can then choose to reject the changes if they violate some of the
conditions being validated.

Validation has transactional semantics. If any validating plugin rejects an
adjustment, creation of the adjusted container will fail and none of the other
related changes will be made.

#### Validation Use Cases

Some key validation uses cases include

1. Functional Validators: These plugins care about the final state and
consistency. They check if the combined effect of all mutations result
in a valid configuration (e.g. are the resource limits sane).

2. Security Validators: These plugins are interested in which plugin is
attempting to modify sensitive fields. They use the extra data passed to
plugins in addition to adjustments to check if a potentially untrusted
plugin tried to modify a restricted field, regardless of the value.
Rejection might occur simply because a non-approved plugin touched a
specific field. Plugins like this may need to be assured to run, and to
have workloads fail-closed if the validator is not running.

3. Mandatory Plugin Validators: These ensure that specific plugins, required
for certain workloads have successfully run. They might use the extra metadata
passed to validator in addition to adjustments to confirm the mandatory
plugin owns certain critical fields and potentially use the list of plugins
that processed the container to ensure all mandatory plugins were consulted.

#### Default Validation

The default built-in validator plugin provides configurable minimal validation.
It may be enabled or disabled by configuring the container runtime. It can be
selectively configured to

1. Reject OCI Hook injection: Reject any adjustment which tries to inject
OCI Hooks into a container.

2. Reject Linux seccomp policy adjustment: Reject any adjustment which tries
to set/override Linux seccomp policy of a container. There are separate controls
for rejecting adjustment of the seccomp policy profile based on the type of policy
profile set for the container. These types include the runtime default seccomp
policy profile, a custom policy profile, and unconfined security profiles.

3. Reject Linux Namespace adjustment: Reject any adjustment which tries to
alter Linux namespaces of a container.

4. Verify global mandatory plugins: Verify that all configured mandatory
plugins are present and have processed a container. Otherwise reject the
creation of the container.

5. Verify annotated mandatory plugins: Verify that an annotated set of
container-specific mandatory plugins are present and have processed a
container. Otherwise reject the creation of the container.

Containers can be annotated to tolerate missing required plugins. This
allows one to deploy mandatory plugins as containers themselves.

#### Default Validation Scope

Currently only OCI hook injection, Linux seccomp policy and Linux namespace
adjustment can be restricted using the default validator. However, this probably
will change in the future. Especially when NRI is extended with control over more
container parameters. If newly added controls will have security implications,
expect corresponding configurable restrictions in the default validator.

## Runtime Adaptation

The NRI [runtime adaptation](pkg/adaptation) package is the interface
runtimes use to integrate to NRI and interact with NRI plugins. It
implements basic plugin discovery, startup and configuration. It also
provides the functions necessary to hook NRI plugins into lifecycle
events of pods and containers from the runtime.

The package hides the fact that multiple NRI plugins might be processing
any single pod or container lifecycle event. It takes care of invoking
plugins in the correct order and combining responses by multiple plugins
into a single one. While combining responses, the package detects any
unintentional conflicting changes made by multiple plugins to a single
container and flags such an event as an error to the runtime.

## Wrapped OCI Spec Generator

The [OCI Spec generator](pkg/runtime-tools/generate) package wraps the
[corresponding package](https://github.com/opencontainers/runtime-tools/tree/master/generate)
and adds functions for applying NRI container adjustments and updates to
OCI Specs. This package can be used by runtime NRI integration code to
apply NRI responses to containers.

## Plugin Stub Library

The plugin stub hides many of the low-level details of implementing an NRI
plugin. It takes care of connection establishment, plugin registration,
configuration, and event subscription. All [sample plugins](plugins)
are implemented using the stub. Any of these can be used as a tutorial on
how the stub library should be used.

## Sample Plugins

The following sample plugins exist for NRI:

  - [logger](plugins/logger)
  - [differ](plugins/differ)
  - [device injector](plugins/device-injector)
  - [network device injector](plugins/network-device-injector)
  - [network logger](plugins/network-logger)
  - [OCI hook injector](plugins/hook-injector)
  - [ulimit adjuster](plugins/ulimit-adjuster)
  - [NRI v0.1.0 plugin adapter](plugins/v010-adapter)
  - [WebAssembly plugin](plugins/wasm)
  - [RDT](plugins/rdt)
  - [template](plugins/template)

Please see the documentation of these plugins for further details
about what and how each of these plugins can be used for.

Ready-built container images for these plugins are available at
ghcr.io/containerd/nri/plugins/<plugin>.

Minimal [kustomize](https://kubernetes.io/docs/tasks/manage-kubernetes-objects/kustomization/)
overlays for deploying the sample are available at
[contrib/kustomize](contrib/kustomize). See plugin-specific documentation for
detailed examples.

> [!CAUTION]
> Use at your own risk. The kustomize overlays provided in this repository is
> offered as a convenience for testing and demonstration purposes.

### WebAssembly support

The NRI supports WebAssembly plugins through a SDK provided by
[knqyf263/go-plugin](https://github.com/knqyf263/go-plugin). This method works
natively from go version 1.24 and works like any other binary plugin by
supporting the same [protocol definition](pkg/api/api.proto). An example plugin
outlining the most basic functionality can be found in
[plugins/wasm](./plugins/wasm/plugin.go). There is no middle layer (stub)
implemented like for the ttRPC plugins for simplicity reasons. If logging from
the WebAssembly plugin is required, then the NRI provides a host function helper
[`Log`](https://github.com/containerd/nri/blob/8ebdb076ea6aa524094a7f1c2c9ca31c30852328/plugins/wasm/plugin.go#L31-L36)
for that.

WebAssembly support is enabled by default. It can be disabled at compile
time using the `nri_no_wasm` build tag.

## Security Considerations

From a security perspective NRI plugins should be considered part of the
container runtime. NRI does not implement granular access control to the
functionality it offers. Access to NRI is controlled by restricting access
to the systemwide NRI socket. If a process can connect to the NRI socket
and send data, it has access to the full scope of functionality available
via NRI.

In particular this includes

  - injection of OCI hooks, which allow for arbitrary execution of processes with the same privilege level as the container runtime
  - arbitrary changes to mounts, including new bind-mounts, changes to the proc, sys, mqueue, shm, and tmpfs mounts
  - the addition or removal of arbitrary devices
  - arbitrary changes to the limits for memory, CPU, block I/O, and RDT resources available, including the ability to deny service by setting limits very low

The same precautions and principles apply to protecting the NRI socket as
to protecting the socket of the runtime itself. Unless it already exists,
NRI itself creates the directory to hold its socket with permissions that
allow access only for the user ID of the runtime process. By default this
limits NRI access to processes running as root (UID 0). Changing the default
socket permissions is strongly advised against. Enabling more permissive
access control to NRI should never be done without fully understanding the
full implications and potential consequences to container security.

### Plugins as Kubernetes DaemonSets

When the runtime manages pods and containers in a Kubernetes cluster, it
is convenient to deploy and manage NRI plugins using Kubernetes DaemonSets.
Among other things, this requires bind-mounting the NRI socket into the
filesystem of a privileged container running the plugin. Similar precautions
apply and the same care should be taken for protecting the NRI socket and
NRI plugins as for the kubelet DeviceManager socket and Kubernetes Device
Plugins.

The cluster configuration should make sure that unauthorized users cannot
bind-mount host directories and create privileged containers which gain
access to these sockets and can act as NRI or Device Plugins. See the
[related documentation](https://kubernetes.io/docs/concepts/security/)
and [best practices](https://kubernetes.io/docs/setup/best-practices/enforcing-pod-security-standards/)
about Kubernetes security.

## API Stability

NRI APIs should not be considered stable yet. We try to avoid unnecessarily
breaking APIs, especially the Stub API which plugins use to interact with NRI.
However, before NRI reaches a stable 1.0.0 release, this is only best effort
and cannot be guaranteed. Meanwhile we do our best to document any API breaking
changes for each release in the [release notes](RELEASES.md).

The current target for a stable v1 API through a 1.0.0 release is the end of
this year.

## Project details

nri is a containerd sub-project, licensed under the [Apache 2.0 license](./LICENSE).
As a containerd sub-project, you will find the:

 * [Project governance](https://github.com/containerd/project/blob/main/GOVERNANCE.md),
 * [Maintainers](./MAINTAINERS),
 * and [Contributing guidelines](https://github.com/containerd/project/blob/main/CONTRIBUTING.md)

information in our [`containerd/project`](https://github.com/containerd/project) repository.
