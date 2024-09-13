# containerd 2.0

Please try out the release binaries available at <https://github.com/containerd/containerd/releases> and report any issues to <https://github.com/containerd/containerd/issues>.

## What's new

### Transfer service is now stable

Proposed in [#7592](https://github.com/containerd/containerd/issues/7592), the transfer service is now stable. The transfer service provides a simple interface to transfer artifact objects between any source and destination. Inspired by the core ideas put forth by the libchan project, the transfer service provides an API with binary streams and data channels as first class citizens to offer a more robust API without the need for constant protocol and API updates.

The transfer service has been integrated with the containerd [client](https://pkg.go.dev/github.com/containerd/containerd/v2@v2.0.0-rc.5/client#Client.Transfer) and the debugging tool `ctr` to pull, push, import and export images to containerd. See [transfer.md](./transfer.md) for more details.

### Sandbox service is now stable

Proposed in [#4131](https://github.com/containerd/containerd/issues/4131), the sandbox service is now stable. The sandbox service extends management of containerd's shim providing more flexibility and functionality for multi-container environments such as pods and VMs.

### Sandboxed CRI is now enabled by default

containerd's CRI plugin has moved from using the legacy CRI server to use the sandbox controller implementation for podsandbox support. As previously noted, the sandbox service has been marked stable.

### Sandbox attributes are now mutable

The sandbox controller has added the `Update` API (`/containerd.services.sandbox.v1.Controller/Update`) to modify a pre-existing sandbox's attributes. i.e. sandbox specification, runtime, extensions, and labels.

### NRI is now enabled by default

NRI is a framework for plugging domain or vendor-specific logic into OCI-compatible container runtimes. It allows users to make changes to containers, perform extra actions, and improve the management of resources. NRI plugins are considered to be part of the container runtime, and access to NRI is controlled by restricting access to the systemwide NRI socket. See [NRI.md](./NRI.md) for more details.

### Daemon configuration version 3

This release adds support for containerd daemon configuration `version = 3`. Daemon configurations are automatically migrated to the latest version on startup. This ensures previous configurations continue to work on future daemons; however, it is recommended to migrate to the latest version to avoid migrations and optimize daemon startup time. See [daemon configuration](../RELEASES.md#daemon-configuration) for more details.

### Plugin info has been added to the Introspection API

This release adds the `PluginInfo` API definition to the introspection service. (`/containerd.services.introspection.v1.Introspection/PluginInfo`)

For example, the new `PluginInfo()` call can be used to inspect the version, features, and annotations of the runtime plugin.

```bash
$ ctr plugins inspect-runtime --runtime=io.containerd.runc.v2 --runc-binary=runc
{
    "Name": "io.containerd.runc.v2",
    "Version": {
        "Version": "v2.0.0-rc.X-XX-gXXXXXXXXX.m",
        "Revision": "v2.0.0-rc.X-XX-gXXXXXXXXX.m"
    },
    "Options": {
        "binary_name": "runc"
    },
    "Features": {
        "ociVersionMin": "1.0.0",
        "ociVersionMax": "1.2.0",
        ...,
    },
    "Annotations": null
}
```

### OTEL environment variable configuration support for containerd's built-in tracing plugin

This release adds support for the standard environment variables defined in the OpenTelemetry [specification](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/) to the built-in containerd tracing plugin.

Implementation note: Both `OTEL_SDK_DISABLED` and one of either `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` must be set in the containerd environment for the tracing plugin to be enabled. The containerd tracing OLTP plugin is disabled if no endpoint is configured.

### Intel ISA-L's igzip support

Intel ISA-L's igzip support has been added to the containerd client. If found, the containerd client uses igzip for gzip decompression, such as when pulling container images. Benchmarks have shown igzip to outperform both Go's built-in gzip and external pigz implementations.

### Deprecation warnings can now be discovered via the Introspection API

Deprecations warnings have been added to the `ServerResponse` for the introspection service (`/containerd.services.introspection.v1.Introspection/Server`) and to the `ctr` tool via `ctr deprecation list`.

To ease containerd 2.0 transition, the community has moved to backport this feature to containerd 1.6 and 1.7 release branches to give users another tool to identify the sticking points for migrating containerd versions.

Administrators whose workloads are running on containerd versions >= 1.6.25, >= 1.7.9 can query their containerd server to receive  warnings for features marked for deprecation and are in the critical path. With the `ctr` client, users can run `ctr deprecations list`.

## What's breaking

### Docker Schema 1 image support is disabled by default

Pulling Docker Schema 1 (`application/vnd.docker.distribution.manifest.v1+json`) images is disabled by default. Users should migrate their container images by rebuilding/pushing with the latest Docker or nerdctl+Buildkit tooling. Previous behavior can be re-enabled by setting an environment variable `CONTAINERD_ENABLE_DEPRECATED_PULL_SCHEMA_1_IMAGE=1` for `containerd` (in the case of CRI) and `ctr`; however, users are **strongly recommended** to migrate to Docker Schema 2 or OCI images. Support for Docker Schema 1 images will be fully removed in a future release.

### `io_uring_*` syscalls are disallowed by default

The following syscalls (`io_uring_enter`, `io_uring_register`, and `io_uring_setup`) have been removed from the default Seccomp profile allowlist. Numerous Linux kernel exploits that are being reported have been linked to `io_uring` to the extent that its usage cannot be recommended as part of the default allowlist.

Additional context:

- <https://security.googleblog.com/2023/06/learnings-from-kctf-vrps-42-linux.html>

### `LimitNOFILE` configuration has been removed

Explicit configuration for `LimitNOFILE` in the reference `containerd.service` systemd service file has been removed. The decision to remove the explicit configuration comes as a result of a community discussion [#8924](https://github.com/containerd/containerd/pull/8924).

containerd's rlimits are inherited by containers, so the daemon's `LimitNOFILE` affects `RLIMIT_NOFILE` within containers. It is recommended to use the default systemd `LimitNOFILE` configuration.

> [!WARNING]
> Administrators on platforms running versions less than systemd 240 should explicitly configure `LimitNOFILE=1024:524288` or risk falling back to the kernel default of `4096`.

### `io.containerd.runtime.v1.linux` and `io.containerd.runc.v1` have been removed

Deprecated in containerd v1.4, support for the Runtime V1 (`io.containerd.runtime.v1.linux`) and Runc V1 (`io.containerd.runc.v1`) shims have been removed. Users should migrate to use `io.containerd.runc.v2` containerd shim instead.

### `containerd.io/restart.logpath` container label has been removed

Deprecated in containerd v1.5, support for the `containerd.io/restart.logpath` container label has been removed. Users should migrate to use `containerd.io/restart.loguri` container label instead.

### CRI v1alpha2 API has been removed

Deprecated in containerd v1.7, support for the CRI v1alpha2 API has been removed. Users should migrate to use CRI v1 instead. k8s users can reference containerd's [kubernetes support](../RELEASES.md#kubernetes-support) matrix to verify support for their Kubernetes version.

### AUFS snapshotter has been removed

Deprecated in containerd v1.7, the built-in `aufs` snapshotter has been removed. As an alternative, it is recommended to use the `overlayfs` snapshotter. See [`snapshotters/README.md`](snapshotters/README.md) for more details.

## What's changing

### containerd client has moved to its own package

The containerd client Go library has moved to its own package ([`github.com/containerd/containerd/v2/client`](https://pkg.go.dev/github.com/containerd/containerd/v2/client)).

See [getting-started.md](./getting-started.md#implementing-your-own-containerd-client) for a working example for building on top of the containerd client.

### CRI registry properties are deprecated

Support for the following properties of `[plugins.\"io.containerd.grpc.v1.cri\".registry]` is deprecated and will be removed in a future release.

- The CRIRegistryMirrors (`mirrors`) property. Users should migrate to use [`config_path`](./hosts.md).
- The CRIRegistryAuths (`auths`) property. Users should migrate to use [`ImagePullSecrets`](https://kubernetes.io/docs/tasks/configure-pod-container/pull-image-private-registry/).
- The CRIRegistryConfigs (`configs`) property. Users should migrate to use [`config_path`](./hosts.md).

### Support for Go-plugin libraries as containerd runtime plugins is deprecated

Go-plugin libraries (`*.so`) support as containerd runtime plugins (sometimes referred as dynamic plugins) is deprecated and will be removed in a future release.

Affected configurations have `plugin_dir` at the root of their containerd configuration.

It is recommended to migrate to use proxy or binary external plugins. See [PLUGINS.md](./PLUGINS.md) for more details.

### Support for events envelope to package containerd events is deprecated

[service.events.Envelope](https://pkg.go.dev/github.com/containerd/containerd/api@v1.7.19/services/events/v1#Envelope) and [ttrpc.events.Envelope](https://pkg.go.dev/github.com/containerd/containerd/api@v1.7.19/services/ttrpc/events/v1#Envelope) support is deprecated and will be removed in a future release. It is recommended for users to migrate to [types.Envelope](https://pkg.go.dev/github.com/containerd/containerd/api/types#Envelope).

### Support for CRI configuration of CNI configuration templates is extended

Previously marked for deprecation in containerd v1.7.0, CRI configuration support for CNI configuration template (`[plugins.\"io.containerd.grpc.v1.cri\".cni.conf_template]`) is extended.
