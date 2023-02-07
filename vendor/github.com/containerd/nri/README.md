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
  - OCI hooks
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

Apart from data identifying the container, these pieces of information
represent the corresponding data in the container's OCI Spec.

### Container Adjustment

During container creation plugins can request changes to the following
container parameters:

  - annotations
  - mounts
  - environment variables
  - OCI hooks
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
configuration, and event subscription. All [sample plugins](pkg/plugins)
are implemented using the stub. Any of these can be used as a tutorial on
how the stub library should be used.

## Sample Plugins

The following sample plugins exist for NRI:

  - [logger](plugins/logger)
  - [differ](plugins/differ)
  - [device injector](plugins/device-injector)
  - [OCI hook injector](plugins/hook-injector)
  - [NRI v0.1.0 plugin adapter](plugins/v010-adapter)

Please see the documentation of these plugins for further details
about what and how each of these plugins can be used for.

## Project details

nri is a containerd sub-project, licensed under the [Apache 2.0 license](./LICENSE).
As a containerd sub-project, you will find the:

 * [Project governance](https://github.com/containerd/project/blob/main/GOVERNANCE.md),
 * [Maintainers](https://github.com/containerd/project/blob/main/MAINTAINERS),
 * and [Contributing guidelines](https://github.com/containerd/project/blob/main/CONTRIBUTING.md)

information in our [`containerd/project`](https://github.com/containerd/project) repository.
