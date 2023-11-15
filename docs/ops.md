# containerd for Ops and Admins

containerd is meant to be a simple daemon to run on any system.
It provides a minimal config with knobs to configure the daemon and what plugins are used when necessary.

```
NAME:
   containerd -
                    __        _                     __
  _________  ____  / /_____ _(_)___  ___  _________/ /
 / ___/ __ \/ __ \/ __/ __ `/ / __ \/ _ \/ ___/ __  /
/ /__/ /_/ / / / / /_/ /_/ / / / / /  __/ /  / /_/ /
\___/\____/_/ /_/\__/\__,_/_/_/ /_/\___/_/   \__,_/

high performance container runtime


USAGE:
   containerd [global options] command [command options] [arguments...]

VERSION:
   v2.0.0-beta.0

DESCRIPTION:

containerd is a high performance container runtime whose daemon can be started
by using this command. If none of the *config*, *publish*, *oci-hook*, or *help* commands
are specified, the default action of the **containerd** command is to start the
containerd daemon in the foreground.


A default configuration is used if no TOML configuration is specified or located
at the default file location. The *containerd config* command can be used to
generate the default configuration for containerd. The output of that command
can be used and modified as necessary as a custom configuration.

COMMANDS:
   config    Information on the containerd config
   publish   Binary to publish events to containerd
   oci-hook  Provides a base for OCI runtime hooks to allow arguments to be injected.
   help, h   Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --config value, -c value     Path to the configuration file (default: "/etc/containerd/config.toml")
   --log-level value, -l value  Set the logging level [trace, debug, info, warn, error, fatal, panic]
   --address value, -a value    Address for containerd's GRPC server
   --root value                 containerd root directory
   --state value                containerd state directory
   --help, -h                   Show help
   --version, -v                Print the version

```

While a few daemon level options can be set from CLI flags the majority of containerd's configuration is kept in the configuration file.
The default path for the config file is located at `/etc/containerd/config.toml`.
You can change this path via the `--config,-c` flags when booting the daemon.

## systemd

If you are using systemd as your init system, which most modern linux OSs are, the service file requires a few modifications.

```systemd
[Unit]
Description=containerd container runtime
Documentation=https://containerd.io
After=network.target

[Service]
ExecStartPre=-/sbin/modprobe overlay
ExecStart=/usr/local/bin/containerd
Delegate=yes
KillMode=process

[Install]
WantedBy=multi-user.target
```

`Delegate=yes` and `KillMode=process` are the two most important changes you need to make in the `[Service]` section.

`Delegate` allows containerd and its runtimes to manage the cgroups of the containers that it creates.
Without setting this option, systemd will try to move the processes into its own cgroups, causing problems for containerd and its runtimes to properly account for resource usage with the containers.

`KillMode` handles when containerd is being shut down.
By default, systemd will look in its named cgroup and kill every process that it knows about for the service.
This is not what we want.
As ops, we want to be able to upgrade containerd and allow existing containers to keep running without interruption.
Setting `KillMode` to `process` ensures that systemd only kills the containerd daemon and not any child processes such as the shims and containers.

The following `systemd-run` command starts containerd in a similar way:
```
sudo systemd-run -p Delegate=yes -p KillMode=process /usr/local/bin/containerd
```

## Base Configuration

In the containerd config file you will find settings for persistent and runtime storage locations as well as grpc, debug, and metrics addresses for the various APIs.

There are a few settings that are important for ops.
The first setting is the `oom_score`.  Because containerd will be managing multiple containers, we need to ensure that containers are killed before the containerd daemon gets into an out of memory condition.
We also do not want to make containerd unkillable, but we want to lower its score to the level of other system daemons.

containerd also exports its own metrics as well as container level metrics via the Prometheus metrics format under `/v1/metrics`.
Currently, Prometheus only supports TCP endpoints, therefore, the metrics address should be a TCP address that your Prometheus infrastructure can scrape metrics from.

containerd also has two different storage locations on a host system.
One is for persistent data and the other is for runtime state.

`root` will be used to store any type of persistent data for containerd.
Snapshots, content, metadata for containers and image, as well as any plugin data will be kept in this location.
The root is also namespaced for plugins that containerd loads.
Each plugin will have its own directory where it stores data.
containerd itself does not actually have any persistent data that it needs to store, its functionality comes from the plugins that are loaded.


```
/var/lib/containerd/
├── io.containerd.content.v1.content
│   ├── blobs
│   └── ingest
├── io.containerd.metadata.v1.bolt
│   └── meta.db
├── io.containerd.runtime.v2.task
│   ├── default
│   └── example
├── io.containerd.snapshotter.v1.btrfs
└── io.containerd.snapshotter.v1.overlayfs
    ├── metadata.db
    └── snapshots
```

`state` will be used to store any type of ephemeral data.
Sockets, pids, runtime state, mount points, and other plugin data that must not persist between reboots are stored in this location.

```
/run/containerd
├── containerd.sock
├── debug.sock
├── io.containerd.runtime.v2.task
│   └── default
│       └── redis
│           ├── config.json
│           ├── init.pid
│           ├── log.json
│           └── rootfs
│               ├── bin
│               ├── data
│               ├── dev
│               ├── etc
│               ├── home
│               ├── lib
│               ├── media
│               ├── mnt
│               ├── proc
│               ├── root
│               ├── run
│               ├── sbin
│               ├── srv
│               ├── sys
│               ├── tmp
│               ├── usr
│               └── var
└── runc
    └── default
        └── redis
            └── state.json
```

Both the `root` and `state` directories are namespaced for plugins.
Both directories are an implementation detail of containerd and its plugins.
They should not be tampered with as corruption and bugs can and will happen.
External apps reading or watching changes in these directories have been known to cause `EBUSY` and stale file handles when containerd and/or its plugins try to cleanup resources.

```toml
version = 2

# persistent data location
root = "/var/lib/containerd"
# runtime state information
state = "/run/containerd"
# set containerd's OOM score
oom_score = -999

# grpc configuration
[grpc]
  address = "/run/containerd/containerd.sock"
  # socket uid
  uid = 0
  # socket gid
  gid = 0

# debug configuration
[debug]
  address = "/run/containerd/debug.sock"
  # socket uid
  uid = 0
  # socket gid
  gid = 0
  # debug level
  level = "info"

# metrics configuration
[metrics]
  # tcp address!
  address = "127.0.0.1:1234"
```

## Plugin Configuration

At the end of the day, containerd's core is very small.
The real functionality comes from plugins.
Everything from snapshotters, runtimes, and content are all plugins that are registered at runtime.
Because these various plugins are so different we need a way to provide type safe configuration to the plugins.
The only way we can do this is via the config file and not CLI flags.

In the config file you can specify plugin level options for the set of plugins that you use via the `[plugins.<name>]` sections.
You will have to read the plugin specific docs to find the options that your plugin accepts.

See [containerd's Plugin documentation](./PLUGINS.md)

### Bolt Metadata Plugin

The bolt metadata plugin allows configuration of the content sharing policy between namespaces.

The default mode "shared" will make blobs available in all namespaces once it is pulled into any namespace.
The blob will be pulled into the namespace if a writer is opened with the "Expected" digest that is already present in the backend.

The alternative mode, "isolated" requires that clients prove they have access to the content by providing all of the content to the ingest before the blob is added to the namespace.

Both modes share backing data, while "shared" will reduce total bandwidth across namespaces, at the cost of allowing access to any blob just by knowing its digest.

The default is "shared". While this is largely the most desired policy, one can change to "isolated" mode with the following configuration:

```toml
version = 2

[plugins."io.containerd.metadata.v1.bolt"]
	content_sharing_policy = "isolated"
```

In "isolated" mode, it is also possible to share only the contents of a specific namespace by adding the label `containerd.io/namespace.shareable=true` to that namespace.
This will make its blobs available in all other namespaces even if the content sharing policy is set to "isolated".
If the label value is set to anything other than `true`, the namespace content will not be shared.
