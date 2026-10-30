# Runtime Handler Configuration

Runtime handlers can be described in a shared configuration directory. The
directory format is available to containerd clients and plugins through the
`core/runtime/config` Go package.

Each handler has a subdirectory containing a `runtime.toml` file:

```text
/etc/containerd/runtimes/
  runc/
    runtime.toml
  kata/
    runtime.toml
```

The common fields are:

```toml
runtime_type = "io.containerd.runc.v2"
runtime_path = "/usr/local/bin/containerd-shim-runc-v2"
snapshotter = "overlayfs"

[options]
  BinaryName = "/usr/local/bin/runc"
  SystemdCgroup = true
```

- `runtime_type` identifies the runtime shim.
- `runtime_path` optionally overrides the shim binary path.
- `snapshotter` optionally selects a snapshotter for the handler.
- `options` contains runtime-specific options.

Consumers may define additional fields in `runtime.toml`. For example, the CRI
plugin accepts the additional fields from its
`containerd.runtimes.<handler>` configuration.

This directory is different from top-level containerd
[`imports`](man/containerd-config.toml.5.md#imports). Imports load complete
containerd configuration fragments, and imported values override values in the
main configuration. The runtime configuration directory contains one runtime
handler per file; each consumer decides how those handlers are merged and when
changes are loaded.

## CRI

Starting with containerd v2.4, the CRI plugin loads runtime handlers from
`/etc/containerd/runtimes` at startup. Runtime handlers in the main
`config.toml` take precedence over handlers with the same name in the runtime
configuration directory.

The directory can be changed in the CRI runtime configuration:

```toml
[plugins.'io.containerd.cri.v1.runtime'.containerd]
  runtime_config_dir = '/etc/containerd/runtimes'
```

A containerd restart is required for the CRI plugin to load changes.
