# /etc/containerd/config.toml 5 07/23/2026

## NAME

containerd-config.toml - configuration file for containerd

## SYNOPSIS

The **config.toml** file is a configuration file for the containerd daemon. The
file must be placed at **/etc/containerd/config.toml** or specified with the
**--config** option of **containerd** to be used by the daemon. If the file
does not exist at the appropriate location or is not provided via the
**--config** option containerd uses its default configuration settings, which
can be displayed with **containerd config default**.

## DESCRIPTION

The TOML file used to configure the containerd daemon settings has a short
list of global settings followed by a series of sections for specific areas
of daemon configuration. There is also a section for **plugins** that allows
each containerd plugin to have an area for plugin-specific configuration and
settings.

## DISCOVERING CONFIGURATION OPTIONS

containerd is plugin-based: each plugin owns its own configuration schema, and
third-party or proxy plugins may add options that are not listed in this man
page. Use **containerd config default** to query available options for all
plugins compiled into the current containerd daemon.

Additional inspection commands:

1. **containerd config default** — print the full default configuration for
   every plugin compiled into the current binary.
2. **containerd config dump** — print the final merged configuration after
   loading **/etc/containerd/config.toml** (or **--config**) and any
   **imports**.
3. **containerd config migrate** — print the active configuration migrated to
   the latest supported version (see __containerd-config(8)__).

For topic guides (CRI, registry hosts, runtimes, plugins, and more), see
https://containerd.io/docs/.

## PLUGIN CONFIGURATION MODEL

Plugin options live under the top-level **[plugins]** table. Each plugin uses
a fully qualified ID as the table key:

```toml
version = 3

[plugins."io.containerd.monitor.v1.cgroups"]
  no_prometheus = false
```

**Plugin IDs** use the form `io.containerd.<area>.vN.<name>`. Large features
may register more than one plugin ID. CRI, for example, uses:

- `io.containerd.cri.v1.images` — image pull, snapshotter, registry
  **config_path**, pinned images
- `io.containerd.cri.v1.runtime` — runtimes, cgroup driver options via runtime
  **options** (for example **SystemdCgroup**)

The CRI section is only used by CRI clients (`kubelet`, `crictl`). Clients
such as `ctr` and `nerdctl` do not read CRI plugin settings.

**disabled_plugins**
: List of plugin IDs that must not be initialized or started.

**required_plugins**
: List of plugin IDs that must load successfully. containerd exits if any
listed plugin is missing or fails to start.

**Loaded plugins**
: Use `ctr plugins ls` to list plugins that actually loaded. A configuration
block for a plugin that is not compiled into the binary has no effect.

**proxy_plugins**
: Register external gRPC plugins by name. Each entry has:

- **type** — one of `snapshot`, `content`, `diff`, or `sandbox`
- **address** — local socket path the daemon connects to
- **platform** (optional) — platform string for the plugin
- **exports** (optional) — string map of values exported to other plugins
- **capabilities** (optional) — list of capability strings advertised by the plugin

```toml
[proxy_plugins.customsnapshot]
  type = "snapshot"
  address = "/var/run/mysnapshotter.sock"
```

## FORMAT

**version**
: The version field in the config file specifies the config’s version. If no
version number is specified inside the config file then it is assumed to be a
version 1 config and parsed as such. Version 4 is the latest config version.
Older configs are automatically migrated on startup.

**root**
: The root directory for containerd metadata. (Default: "/var/lib/containerd")

**state**
: The state directory for containerd (Default: "/run/containerd")

**plugin_dir**
: The directory for dynamic plugins to be stored

**[grpc]** *(deprecated in version 4)*
: Section for gRPC socket listener settings. In version 4, use the server
plugins **io.containerd.server.v1.grpc** and **io.containerd.server.v1.grpc-tcp**
instead. Existing configs are migrated automatically. Contains the following
properties:

- **address** (Default: "/run/containerd/containerd.sock")
- **tcp_address**
- **tcp_tls_cert**
- **tcp_tls_key**
- **uid** (Default: 0)
- **gid** (Default: 0)
- **max_recv_message_size**
- **max_send_message_size**

**[ttrpc]** *(deprecated in version 4)*
: Section for TTRPC settings. In version 4, use the server plugin
**io.containerd.server.v1.ttrpc** instead. In prior versions, when the TTRPC
address was not explicitly set it was derived from the GRPC address
(grpcAddress + ".ttrpc") and inherited GRPC’s UID/GID. In version 4, each
server plugin is independently configured; the TTRPC plugin uses its own
defaults when its configuration block is omitted. Contains properties:

- **address** (Default: "")
- **uid** (Default: 0)
- **gid** (Default: 0)

**[debug]** *(deprecated in version 4)*
: Section to enable and configure a debug socket listener. In version 4, use the
server plugin **io.containerd.server.v1.debug** instead. Contains properties:

- **address** (Default: "") Debug endpoint does not listen by default
- **uid** (Default: 0)
- **gid** (Default: 0)
- **level** (Default: "info") sets the debug log level. Supported levels are:
  "trace", "debug", "info", "warn", "error", "fatal", "panic"
- **format** (Default: "text") sets log format. Supported formats are "text" and "json"

**[metrics]** *(deprecated in version 4)*
: Section to enable and configure a metrics listener. In version 4, use the
server plugin **io.containerd.server.v1.metrics** instead. Contains properties:

- **address** (Default: "") Metrics endpoint does not listen by default
- **grpc_histogram** (Default: false) Turn on or off gRPC histogram metrics

**disabled_plugins**
: Disabled plugins are IDs of plugins to disable. Disabled plugins won't be
initialized and started.

**required_plugins**
: Required plugins are IDs of required plugins. Containerd exits if any
required plugin doesn't exist or fails to be initialized or started.

**[plugins]**
: The plugins section contains configuration options exposed from installed plugins.
The following plugins are enabled by default and their settings are shown below.
Plugins that are not enabled by default will provide their own configuration values
documentation. Use **containerd config default** for the complete list on the
installed binary.

- **[plugins."io.containerd.server.v1.grpc"]** configures the main gRPC server listener (version 4):
  - **address** (Default: "/run/containerd/containerd.sock")
  - **uid** (Default: effective UID)
  - **gid** (Default: effective GID)
  - **max_recv_message_size** (Default: 16777216)
  - **max_send_message_size** (Default: 16777216)
- **[plugins."io.containerd.server.v1.grpc-tcp"]** configures the TCP gRPC server listener (version 4).
  Skipped if address is empty:
  - **address** (Default: "")
  - **tls_cert**, **tls_key**, **tls_ca**, **tls_common_name**
  - **max_recv_message_size** (Default: 16777216)
  - **max_send_message_size** (Default: 16777216)
- **[plugins."io.containerd.server.v1.ttrpc"]** configures the TTRPC server listener (version 4).
  In version 4, this plugin is configured independently from the gRPC plugin.
  If the plugin block is omitted, the TTRPC server binds to its own default
  address rather than deriving one from the gRPC address:
  - **address** (Default: "/run/containerd/containerd.sock.ttrpc")
  - **uid** (Default: effective UID)
  - **gid** (Default: effective GID)
- **[plugins."io.containerd.server.v1.debug"]** configures the debug server listener (version 4).
  Skipped if address is empty:
  - **address** (Default: "")
  - **uid** (Default: 0)
  - **gid** (Default: 0)
- **[plugins."io.containerd.server.v1.metrics"]** configures the metrics HTTP listener (version 4).
  Skipped if address is empty:
  - **address** (Default: "")
- **[plugins."io.containerd.monitor.v1.cgroups"]** has one option __no_prometheus__ (Default: **false**)
- **[plugins."io.containerd.service.v1.diff-service"]** has one option __default__, a list by default set to **["walking"]**
- **[plugins."io.containerd.gc.v1.scheduler"]** has several options that perform advanced tuning for the scheduler:
  - **pause_threshold** is the maximum amount of time GC should be scheduled (Default: **0.02**),
  - **deletion_threshold** guarantees GC is scheduled after n number of deletions (Default: **0** [not triggered]),
  - **mutation_threshold** guarantees GC is scheduled after n number of database mutations (Default: **100**),
  - **schedule_delay** defines the delay after trigger event before scheduling a GC (Default **"0ms"** [immediate]),
  - **startup_delay** defines the delay after startup before scheduling a GC (Default **"100ms"**)
- **[plugins."io.containerd.runtime.v2.task"]** specifies options for configuring the runtime shim:
  - **platforms** specifies the list of supported platforms
  - **sched_core** Core scheduling is a feature that allows only trusted tasks
    to run concurrently on cpus sharing compute resources (eg: hyperthreads on
    a core). (Default: **false**)
- **[plugins."io.containerd.service.v1.tasks-service"]** has performance options:
  - **blockio_config_file** (Linux only) specifies path to blockio class definitions
    (Default: **""**). Controls I/O scheduler priority and bandwidth throttling.
    See [blockio configuration](https://github.com/intel/goresctrl/blob/main/doc/blockio.md#configuration)
    for details of the file format.
  - **rdt_config_file** (Linux only) specifies path to a configuration used for configuring
    RDT (Default: **""**). Enables support for Intel RDT, a technology
    for cache and memory bandwidth management.
    See [RDT configuration](https://github.com/intel/goresctrl/blob/main/doc/rdt.md#configuration)
    for details of the file format.
- **[plugins."io.containerd.cri.v1.images"]** CRI image service:
  - **snapshotter** (Default: **"overlayfs"**) default snapshotter for CRI image unpack
  - **[plugins."io.containerd.cri.v1.images".registry]** registry configuration
    - **config_path** — directory of per-host **hosts.toml** files (for example
      `"/etc/containerd/certs.d"`). See https://containerd.io/docs/ for the
      hosts.toml format (mirrors, CA certs, and client certificates).
- **[plugins."io.containerd.cri.v1.runtime"]** CRI runtime service:
  - **[plugins."io.containerd.cri.v1.runtime".containerd]**
    - **default_runtime_name** (Default: **"runc"**) specifies the default runtime name
  - **[plugins."io.containerd.cri.v1.runtime".containerd.runtimes]** one or more
    container runtimes, each with a unique name
  - **[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.\<runtime\>]**
    a runtime named `<runtime>`:
    - **runtime_type** — containerd runtime v2 shim type (see **runtime_type** below)
  - **[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.\<runtime\>.options]**
    shim-specific options:
    - **BinaryName** — path to the OCI runtime invoked by the shim, e.g. `"/usr/bin/runc"`
    - **SystemdCgroup** — when true, use the systemd cgroup driver
- **[plugins."io.containerd.transfer.v1.local"]** transfer service used for image
  pull and push. See https://containerd.io/docs/.

### runtime_type

Under each named CRI runtime, **runtime_type** selects the containerd runtime
v2 **shim** implementation (not the OCI runtime binary itself):

- `io.containerd.runc.v2` — standard runc-compatible shim (default)
- Other shims (for example gVisor `io.containerd.runsc.v1`, Kata) register
  their own type strings

The OCI runtime binary path is a shim-specific option, commonly **BinaryName**
(or **BinaryPath**) under the runtime's **options** table. Changing only
**BinaryName** keeps the runc shim but invokes a different OCI runtime;
changing **runtime_type** switches to a different shim implementation.

**oom_score**
: The out of memory (OOM) score applied to the containerd daemon process (Default: 0)

**[cgroup]**
: Section for Linux cgroup specific settings

- **path** (Default: "") Specify a custom cgroup path for created containers

**[proxy_plugins]**
: Proxy plugins configure external plugins reached over gRPC. Each named entry
under **[proxy_plugins]** accepts:

- **type** — accepted values: `snapshot`, `content`, `diff`, `sandbox`
- **address** — local socket path
- **platform** (optional)
- **exports** (optional)
- **capabilities** (optional)

**timeouts**
: Timeouts specified as a duration

<!-- [timeouts]
  "io.containerd.timeout.shim.cleanup" = "5s"
  "io.containerd.timeout.shim.load" = "5s"
  "io.containerd.timeout.shim.shutdown" = "3s"
  "io.containerd.timeout.task.state" = "2s" -->

**imports**
: Imports is a list of additional configuration files to include.
This allows to split the main configuration file and keep some sections
separately (for example vendors may keep a custom runtime configuration in a
separate file without modifying the main `config.toml`).
Imported files will overwrite simple fields like `int` or
`string` (if not empty) and will append `array` and `map` fields.
Imported files are also versioned, and the version can't be higher than
the main config.

**stream_processors**

- **accepts** (Default: "[]") Accepts specific media-types
- **returns** (Default: "") Returns the media-type
- **path** (Default: "") Path or name of the binary
- **args** (Default: "[]") Args to the binary

## EXAMPLES

### Version 4 Configuration

The following is a **config.toml** example using version 4, where server
settings are configured as plugins:

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

### Multiple Runtimes

The following is an example partial configuration with two CRI runtimes:

```toml
version = 3

[plugins."io.containerd.cri.v1.runtime".containerd]
  default_runtime_name = "runc"

  [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.runc]
    privileged_without_host_devices = false
    runtime_type = "io.containerd.runc.v2"

    [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.runc.options]
      BinaryName = "/usr/bin/runc"

  [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.other]
    privileged_without_host_devices = false
    runtime_type = "io.containerd.runc.v2"

    [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.other.options]
      BinaryName = "/usr/bin/path-to-runtime"
```

The above creates two named runtime configurations — `runc` and `other` — and
sets the default runtime to `runc`. These entries are used only for runtimes
invoked via CRI. To use the non-default `other` runtime, a Kubernetes
RuntimeClass / CRI `runtime_handler` named `other` selects that named config.

The CRI specification includes a
[`runtime_handler` field](https://github.com/kubernetes/cri-api/blob/de5f1318aede866435308f39cb432618a15f104e/pkg/apis/runtime/v1/api.proto#L476),
which references the named runtime.

Runtimes are under
`[plugins."io.containerd.cri.v1.runtime".containerd.runtimes]`, with each
runtime given a unique name. Shim-specific options live under
`[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.<runtime>.options]`.

**runtime_type** selects the containerd runtime v2 **shim** (here
`io.containerd.runc.v2`). **BinaryName** is a shim-specific option that points
at the OCI runtime binary the shim should execute (`/usr/bin/runc` vs
`/usr/bin/path-to-runtime` in the example).

### Registry hosts.toml

Point CRI at a directory of per-host **hosts.toml** files for mirrors, CA
certs, and client certificates:

```toml
version = 3

[plugins."io.containerd.cri.v1.images".registry]
  config_path = "/etc/containerd/certs.d"
```

See https://containerd.io/docs/ for the hosts.toml format.

### Proxy plugin

```toml
version = 3

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

Online documentation: https://containerd.io/docs/
