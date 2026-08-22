# /etc/containerd/config.toml 5 07/25/2026

## NAME

containerd-config.toml - configuration file for containerd

## SYNOPSIS

The **config.toml** file is a configuration file for the containerd daemon.
The file must be placed at **/etc/containerd/config.toml** or specified with
the **--config** option of **containerd**. If no file is provided, containerd
uses its default configuration, shown by **containerd config default**.

## DESCRIPTION

The TOML file has global settings, optional sections for daemon behaviour,
and a **[plugins]** table for plugin-specific options.

containerd is plugin-based: each plugin owns its own configuration schema,
and third-party or proxy plugins may add options that are not listed in this
man page. Use **containerd config default** to query available options for
all plugins compiled into the current containerd daemon.

Additional commands (see __containerd-config(8)__):

- **containerd config dump** — print the merged active configuration
- **containerd config migrate** — alias of **dump** (latest config version)

Documentation: https://containerd.io/docs/

## FORMAT

**version**
: Config file version. If omitted, the file is parsed as version 1.
Version **4** is the latest. Older configs are migrated on startup.

**root**
: Root directory for containerd metadata. (Default: "/var/lib/containerd")

**state**
: State directory. (Default: "/run/containerd")

**plugin_dir**
: Directory for dynamic plugins.

**disabled_plugins**
: List of plugin IDs that must not be initialized or started.

**required_plugins**
: List of plugin IDs that must load successfully. containerd exits if any
listed plugin is missing or fails to start.

**oom_score**
: OOM score for the containerd process. (Default: 0)

**[cgroup]**
: Linux cgroup settings.

- **path** (Default: "") custom cgroup path for created containers

**[plugins]**
: Plugin configuration. Keys are fully qualified plugin IDs of the form
`io.containerd.<area>.vN.<name>`. List loaded plugins with `ctr plugins ls`.
A configuration block for a plugin not present in the binary has no effect.

The following plugins are enabled by default; settings below match
**containerd config default** for a typical build. Other plugins document
their own options (see https://containerd.io/docs/).

- **[plugins."io.containerd.server.v1.grpc"]** main gRPC listener:
  - **address** (Default: "/run/containerd/containerd.sock")
  - **uid** (Default: effective UID)
  - **gid** (Default: effective GID)
  - **max_recv_message_size** (Default: 16777216)
  - **max_send_message_size** (Default: 16777216)
- **[plugins."io.containerd.server.v1.grpc-tcp"]** TCP gRPC listener (skipped
  if **address** is empty):
  - **address** (Default: "")
  - **tls_cert**, **tls_key**, **tls_ca**, **tls_common_name**
  - **max_recv_message_size** (Default: 16777216)
  - **max_send_message_size** (Default: 16777216)
- **[plugins."io.containerd.server.v1.ttrpc"]** TTRPC listener (configured
  independently of gRPC; omitted block uses the plugin defaults):
  - **address** (Default: "/run/containerd/containerd.sock.ttrpc")
  - **uid** (Default: effective UID)
  - **gid** (Default: effective GID)
- **[plugins."io.containerd.server.v1.debug"]** debug listener (skipped if
  **address** is empty):
  - **address** (Default: "")
  - **uid** (Default: 0)
  - **gid** (Default: 0)
- **[plugins."io.containerd.server.v1.metrics"]** metrics HTTP listener
  (skipped if **address** is empty):
  - **address** (Default: "")
- **[plugins."io.containerd.monitor.v1.cgroups"]**
  - **no_prometheus** (Default: **false**)
- **[plugins."io.containerd.service.v1.diff-service"]**
  - **default** (Default: **["walking"]**)
- **[plugins."io.containerd.gc.v1.scheduler"]**
  - **pause_threshold** (Default: **0.02**)
  - **deletion_threshold** (Default: **0**)
  - **mutation_threshold** (Default: **100**)
  - **schedule_delay** (Default: **"0ms"**)
  - **startup_delay** (Default: **"100ms"**)
- **[plugins."io.containerd.runtime.v2.task"]**
  - **platforms** — supported platforms
  - **sched_core** (Default: **false**) core scheduling
- **[plugins."io.containerd.service.v1.tasks-service"]**
  - **blockio_config_file** (Linux; Default: **""**)
  - **rdt_config_file** (Linux; Default: **""**)
- **[plugins."io.containerd.cri.v1.images"]** CRI image service (kubelet /
  crictl only; not used by **ctr** / **nerdctl**):
  - **snapshotter** (Default: **"overlayfs"**)
  - **[plugins."io.containerd.cri.v1.images".registry]**
    - **config_path** — directory of per-host **hosts.toml** files (for
      example `"/etc/containerd/certs.d"`). Format:
      https://containerd.io/docs/
- **[plugins."io.containerd.cri.v1.runtime"]** CRI runtime service:
  - **[plugins."io.containerd.cri.v1.runtime".containerd]**
    - **default_runtime_name** (Default: **"runc"**)
  - **[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.\<name\>]**
    - **runtime_type** — containerd runtime v2 shim type (for example
      `io.containerd.runc.v2`)
    - **options** — shim-specific options, commonly:
      - **BinaryName** — path to the OCI runtime binary
      - **SystemdCgroup** — use the systemd cgroup driver when true

**[proxy_plugins]**
: External plugins reached over gRPC. Each named entry accepts:

- **type** — short name (`snapshot`, `content`, `diff`, `sandbox`) or the
  fully qualified plugin type string (for example
  `io.containerd.snapshotter.v1`, `io.containerd.content.v1`,
  `io.containerd.differ.v1`, `io.containerd.sandbox.controller.v1`)
- **address** — local socket path
- **platform** (optional)
- **exports** (optional) — string map
- **capabilities** (optional) — list of capability strings

**timeouts**
: Map of timeout name to duration string. Duration values use Go
**time.ParseDuration** syntax (for example **"5s"**, **"100ms"**, **"1m"**).
Default keys (see **containerd config default**):

- **io.containerd.timeout.shim.cleanup**
- **io.containerd.timeout.shim.load**
- **io.containerd.timeout.shim.shutdown**
- **io.containerd.timeout.task.state**
- **io.containerd.timeout.bolt.open**
- **io.containerd.timeout.metrics.shimstats**
- **io.containerd.timeout.cri.defercleanup**

Example:

```toml
[timeouts]
  "io.containerd.timeout.shim.cleanup" = "5s"
  "io.containerd.timeout.shim.load" = "5s"
  "io.containerd.timeout.shim.shutdown" = "3s"
  "io.containerd.timeout.task.state" = "2s"
```

**imports**
: List of additional configuration files to include. Imported files overwrite
non-empty simple fields and append array/map fields. Imported files are
versioned; their version must not be higher than the main config.

**stream_processors**
: Named stream processors. Each entry is a table under
**[stream_processors.\<id\>]** with:

- **accepts** — list of accepted media types (Default: [])
- **returns** — media type produced (Default: "")
- **path** — processor binary path or name (Default: "")
- **args** — arguments passed to the binary (Default: [])

### Deprecated top-level server sections

The following top-level tables are deprecated. Prefer the
**io.containerd.server.v1.*** plugins listed above. Existing configs are
migrated automatically on startup.

**[grpc]**
: Legacy gRPC socket settings. Prefer **plugins."io.containerd.server.v1.grpc"**
and **plugins."io.containerd.server.v1.grpc-tcp"**.

- **address** (Default: "/run/containerd/containerd.sock")
- **tcp_address**, **tcp_tls_cert**, **tcp_tls_key**
- **uid** (Default: 0), **gid** (Default: 0)
- **max_recv_message_size**, **max_send_message_size**

**[ttrpc]**
: Legacy TTRPC settings. Prefer **plugins."io.containerd.server.v1.ttrpc"**.

- **address** (Default: "")
- **uid** (Default: 0), **gid** (Default: 0)

**[debug]**
: Legacy debug socket. Prefer **plugins."io.containerd.server.v1.debug"**.

- **address** (Default: "")
- **uid** (Default: 0), **gid** (Default: 0)
- **level** (Default: "info") — "trace", "debug", "info", "warn", "error",
  "fatal", "panic"
- **format** (Default: "text") — "text" or "json"

**[metrics]**
: Legacy metrics listener. Prefer **plugins."io.containerd.server.v1.metrics"**.

- **address** (Default: "")
- **grpc_histogram** (Default: false)

## EXAMPLES

### Version 4 configuration

```toml
version = 4

root = "/var/lib/containerd"
state = "/run/containerd"
oom_score = 0
imports = ["/etc/containerd/runtime_*.toml", "./debug.toml"]

[plugins."io.containerd.server.v1.grpc"]
  address = "/run/containerd/containerd.sock"

[plugins."io.containerd.server.v1.ttrpc"]
  address = "/run/containerd/containerd.sock.ttrpc"

[plugins."io.containerd.server.v1.debug"]
  address = "/run/containerd/debug.sock"
  level = "info"

[cgroup]
  path = ""

[plugins]
  [plugins."io.containerd.monitor.v1.cgroups"]
    no_prometheus = false
  [plugins."io.containerd.service.v1.diff-service"]
    default = ["walking"]
  [plugins."io.containerd.gc.v1.scheduler"]
    pause_threshold = 0.02
    deletion_threshold = 0
    mutation_threshold = 100
    schedule_delay = 0
    startup_delay = "100ms"
  [plugins."io.containerd.runtime.v2.task"]
    platforms = ["linux/amd64"]
    sched_core = true
  [plugins."io.containerd.service.v1.tasks-service"]
    blockio_config_file = ""
    rdt_config_file = ""
```

### CRI: two runtimes

```toml
version = 4

[plugins."io.containerd.cri.v1.runtime".containerd]
  default_runtime_name = "runc"

  [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.runc]
    runtime_type = "io.containerd.runc.v2"

    [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.runc.options]
      BinaryName = "/usr/bin/runc"

  [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.other]
    runtime_type = "io.containerd.runc.v2"

    [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.other.options]
      BinaryName = "/usr/bin/path-to-runtime"
```

Fields:

- **runtime_type** — containerd runtime v2 shim type
- **BinaryName** — path to the OCI runtime binary (shim option)
- CRI clients select a named runtime with the CRI **runtime_handler**
  field (Kubernetes RuntimeClass)

### CRI: registry hosts.toml

```toml
version = 4

[plugins."io.containerd.cri.v1.images".registry]
  config_path = "/etc/containerd/certs.d"
```

Place per-host **hosts.toml** files under that directory. Format:
https://containerd.io/docs/

### Proxy plugins

```toml
version = 4

[proxy_plugins.customsnapshot]
  type = "snapshot"
  address = "/var/run/mysnapshotter.sock"

[proxy_plugins.mysandbox]
  type = "sandbox"
  address = "/var/run/mysandbox.sock"
```

## BUGS

Please file any specific issues that you encounter at
https://github.com/containerd/containerd.

## AUTHOR

Phil Estes <estesp@gmail.com>

## SEE ALSO

ctr(8), containerd-config(8), containerd(8)

https://containerd.io/docs/
