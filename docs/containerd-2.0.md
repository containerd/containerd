# containerd 2.0

Please try out the release binaries available at <https://github.com/containerd/containerd/releases> and report any issues to <https://github.com/containerd/containerd/issues>.

## What's new

### Transfer service is now stable

Proposed in [#7592](https://github.com/containerd/containerd/issues/7592), the transfer service is now stable. The transfer service provides a simple interface to transfer artifact objects between any source and destination. Inspired by the core ideas put forth by the libchan project, the transfer service provides an API with binary streams and data channels as first class citizens to offer a more robust API without the need for constant protocol and API updates.

The transfer service has been integrated with the containerd [`client.Transfer`](https://pkg.go.dev/github.com/containerd/containerd/v2@v2.0.0-rc.5/client#Client.Transfer) and the debugging tool `ctr` to pull, push, import and export images to containerd. See the ["Transfer Service"](./transfer.md) document for more details.

### Sandbox service is now stable

Proposed in [#4131](https://github.com/containerd/containerd/issues/4131), the sandbox service is now stable. The sandbox service extends management of containerd's shim providing more flexibility and functionality for multi-container environments such as pods and VMs.

### Sandboxed CRI is now enabled by default

containerd's CRI plugin has moved from using the legacy CRI server to use the sandbox controller implementation for podsandbox support. As previously noted, the sandbox service has been marked stable.

### Sandbox attributes are now mutable

The sandbox controller has added the `Update` API (`/containerd.services.sandbox.v1.Controller/Update`) to modify a pre-existing sandbox's attributes. i.e. sandbox specification, runtime, extensions, and labels.

### NRI is now enabled by default

NRI (Node Resource Interface) is a framework for plugging domain or vendor-specific logic into OCI-compatible container runtimes. It allows users to make changes to containers, perform extra actions, and improve the management of resources. NRI plugins are considered to be part of the container runtime, and access to NRI is controlled by restricting access to the systemwide NRI socket. See the ["NRI"](NRI.md) document for more details.

### CDI is now enabled by default

CDI (Container Device Interface) provides a standard mechanism for device vendors to describe what is required to provide access to a specific resource such as a GPU beyond a simple device name.
CDI is now part of the Kubernetes Device Plugin framework.
See [the Kubernetes Enhancement Proposal 4009](https://github.com/kubernetes/enhancements/tree/master/keps/sig-node/4009-add-cdi-devices-to-device-plugin-api).

### Daemon configuration version 3

This release adds support for containerd daemon configuration `version = 3`. Daemon configurations are automatically migrated to the latest version on startup. This ensures previous configurations continue to work on future daemons; however, it is recommended to migrate to the latest version to avoid migrations and optimize daemon startup time. See ["Daemon configuration"](../RELEASES.md#daemon-configuration) in the releases document for more details.

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

### Image verifier plugins

The transfer service now supports plugins that can verify that images are allowed to be pulled. Plugins like this can implement policy, such as enforcing that container images are signed, or that images must have particular names. Plugins are independent programs that communicate via command-line arguments and standard I/O. See more details in [the image verifier plugin documentation](image-verification.md).

### CRI support for user namespaces

The CRI plugin now supports running pods with [user namespaces](https://kubernetes.io/docs/concepts/workloads/pods/user-namespaces/) so as to map the user IDs in pods to different user IDs on the host.
This enables isolation of the root user inside the container, constraining available permissions on the host further than seccomp and capabilities alone.

This features needs [runc](https://github.com/opencontainers/runc) v1.2.0 or later.

### CRI support for recursive read-only mounts

The CRI plugin now supports [recursive read-only mounts](https://kubernetes.io/docs/concepts/storage/volumes/#read-only-mounts) so as to prohibit accidentally having writable submounts.

### Unprivileged ports and ICMP by default for CRI

The CRI plugin now enables `net.ipv4.ip_unprivileged-port-start=0` and `net.ipv4.ping_group_range=0 2147483647` for containers that do not use the host network namespace or user namespaces.  This enables containers to bind to ports below 1024 without granting `CAP_NET_BIND_SERVICE` and to run `ping` without `CAP_NET_RAW`.  This default behavior change can be reverted by setting the `enable_unprivileged_ports` and `enable_unprivileged_icmp` options to `false` in the CRI plugin configuration.

### Deprecation warnings can now be discovered via the Introspection API

Deprecations warnings have been added to the `ServerResponse` for the introspection service (`/containerd.services.introspection.v1.Introspection/Server`) and to the `ctr` tool via `ctr deprecation list`.

To ease containerd 2.0 transition, the community has moved to backport this feature to containerd 1.6 and 1.7 release branches to give users another tool to identify the sticking points for migrating containerd versions.

Administrators whose workloads are running on containerd versions >= 1.6.27, >= 1.7.12 can query their containerd server to receive warnings for features marked for deprecation and are in the critical path. With the `ctr` client, users can run `ctr deprecations list` (and optionally pass `--format json` for machine-readable output).

## What's breaking

### Docker Schema 1 image support is disabled by default

Pulling Docker Schema 1 (`application/vnd.docker.distribution.manifest.v1+prettyjws`) images is disabled by default. Users should migrate their container images by rebuilding/pushing with the latest Docker or nerdctl+Buildkit tooling. Previous behavior can be re-enabled by setting an environment variable `CONTAINERD_ENABLE_DEPRECATED_PULL_SCHEMA_1_IMAGE=1` for `containerd` (in the case of CRI clients, such as Kubernetes and `crictl`) and `ctr` (`ctr` users also must specify `--local`); however, users are **strongly recommended** to migrate to Docker Schema 2 or OCI images. Support for Docker Schema 1 images will be fully removed in a future release.

Since containerd 1.7.8 and 1.6.25, schema 1 images are labeled during pull with `io.containerd.image/converted-docker-schema1`. To find images that were converted from schema 1, you can use a command like `ctr namespaces list --quiet | xargs -I{} -- ctr --namespace={} image list 'labels."io.containerd.image/converted-docker-schema1"'`.

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

Deprecated in containerd v1.7, support for the CRI v1alpha2 API has been removed. Users should migrate to use CRI v1 instead. k8s users can reference containerd's ["Kubernetes support"](../RELEASES.md#kubernetes-support) matrix to verify support for their Kubernetes version.

### AUFS snapshotter has been removed

Deprecated in containerd v1.5, the built-in `aufs` snapshotter has been removed. As an alternative, it is recommended to use the `overlayfs` snapshotter. See the ["Snapshotters"](snapshotters/README.md) document for more details.

### `cri-containerd-(cni-)-VERSION-OS-ARCH.tar.gz` release bundles have been removed

Deprecated in containerd v1.7, the `cri-containerd-(cni-)-VERSION-OS-ARCH.tar.gz` release bundles have been removed from
<https://github.com/containerd/containerd/releases>.

Instead of this, install the following components separately, either from the binary or from the source:
* [containerd (`containerd-VERSION-OS-ARCH.tar.gz`)](https://github.com/containerd/containerd/releases)
* [runc](https://github.com/opencontainers/runc/releases)
* [CNI plugins](https://github.com/containernetworking/plugins/releases)

The CRI plugin has been included in containerd since containerd 1.1.

See also the ["Getting started"](./getting-started.md) document.

## What's changing

### containerd client has moved to its own package

The containerd client Go library has moved to its own package ([`github.com/containerd/containerd/v2/client`](https://pkg.go.dev/github.com/containerd/containerd/v2/client)).

See ["Implementing your own containerd client"](./getting-started.md#implementing-your-own-containerd-client) from the getting started guide for a working example for building on top of the containerd client.

### CRI registry properties are deprecated

Support for the following properties of `[plugins.\"io.containerd.grpc.v1.cri\".registry]` is deprecated and will be removed in a future release.

- The CRIRegistryMirrors (`mirrors`) property. Users should migrate to use [`config_path`](./hosts.md).
- The CRIRegistryAuths (`auths`) property. Users should migrate to use [`ImagePullSecrets`](https://kubernetes.io/docs/tasks/configure-pod-container/pull-image-private-registry/).
- The CRIRegistryConfigs (`configs`) property. Users should migrate to use [`config_path`](./hosts.md).

### Support for Go-plugin libraries as containerd runtime plugins is deprecated

Go-plugin libraries (`*.so`) support as containerd runtime plugins (sometimes referred as dynamic plugins) is deprecated and will be removed in a future release.

Affected configurations have `plugin_dir` at the root of their containerd configuration.

It is recommended to migrate to use proxy or binary external plugins. See the ["containerd Plugins"](./PLUGINS.md) document for more details.

### Support for events envelope to package containerd events is deprecated

[`service.events.Envelope`](https://pkg.go.dev/github.com/containerd/containerd/api@v1.7.19/services/events/v1#Envelope) and [`ttrpc.events.Envelope`](https://pkg.go.dev/github.com/containerd/containerd/api@v1.7.19/services/ttrpc/events/v1#Envelope) support is deprecated and will be removed in a future release. It is recommended for users to migrate to [`types.Envelope`](https://pkg.go.dev/github.com/containerd/containerd/api/types#Envelope).

### Support for CRI configuration of CNI configuration templates is extended

Previously marked for deprecation in containerd v1.7.0, CRI configuration support for CNI configuration template (`[plugins.\"io.containerd.grpc.v1.cri\".cni.conf_template]`) is extended.
