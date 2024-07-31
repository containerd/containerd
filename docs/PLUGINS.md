# containerd Plugins

containerd supports extending its functionality using most of its defined
interfaces. This includes using a customized runtime, snapshotter, content
store, and even adding gRPC interfaces.

## Smart Client Model

containerd has a smart client architecture, meaning any functionality which is
not required by the daemon is done by the client. This includes most high
level interactions such as creating a container's specification, interacting
with an image registry, or loading an image from tar. containerd's Go client
gives a user access to many points of extensions from creating their own
options on container creation to resolving image registry names.

See [containerd's Go documentation](https://godoc.org/github.com/containerd/containerd/v2/client)

## External Plugins

External plugins allow extending containerd's functionality using an officially
released version of containerd without needing to recompile the daemon to add a
plugin.

containerd allows extensions through two methods:
 - via a binary available in containerd's PATH
 - by configuring containerd to proxy to another gRPC service

### V2 Runtimes

containerd supports multiple container runtimes. Each container can be
invoked with a different runtime.

When using the Container Runtime Interface (CRI) plugin, named runtimes can be defined
in the containerd configuration file. When a container is run without specifying a runtime,
the configured default runtime is used. Alternatively, a different named runtime can be
specified explicitly when creating a container via CRI gRPC by selecting the runtime handler to be used.

When a client such as `ctr` or `nerdctl` creates a container, it can optionally specify a runtime and options to use.
If a runtime is not specified, containerd will use its default runtime.

containerd invokes v2 runtimes as binaries on the system,
which are used to start the shim process for containerd. This, in turn, allows
containerd to start and manage those containers using the runtime shim api returned by
the binary.

For more details on runtimes and shims, including how to invoke and configure them,
see the [runtime v2 documentation](../core/runtime/v2/README.md)

### Proxy Plugins

A proxy plugin is configured using containerd's config file and will be loaded
alongside the internal plugins when containerd is started. These plugins are
connected to containerd using a local socket serving one of containerd's gRPC
API services. Each plugin is configured with a type and name just as internal
plugins are.

#### Configuration

Update the containerd config file, which by default is at
`/etc/containerd/config.toml`. Add a `[proxy_plugins]` section along with a
section for your given plugin `[proxy_plugins.myplugin]`. The `address` must
refer to a local socket file which the containerd process has access to. The
currently supported types are `snapshot`, `content`, and `diff`.

```toml
version = 2

[proxy_plugins]
  [proxy_plugins.customsnapshot]
    type = "snapshot"
    address = "/var/run/mysnapshotter.sock"
```

#### Implementation

Implementing a proxy plugin is as easy as implementing the gRPC API for a
service. For implementing a proxy plugin in Go, look at the go doc for
[content store service](https://godoc.org/github.com/containerd/containerd/v2/api/services/content/v1#ContentServer), [snapshotter service](https://godoc.org/github.com/containerd/containerd/v2/api/services/snapshots/v1#SnapshotsServer), and [diff service](https://pkg.go.dev/github.com/containerd/containerd/v2/api/services/diff/v1#DiffServer).

The following example creates a snapshot plugin binary which can be used
with any implementation of
[containerd's Snapshotter interface](https://godoc.org/github.com/containerd/containerd/v2/snapshots#Snapshotter)
```go
package main

import (
	"fmt"
	"net"
	"os"

	"google.golang.org/grpc"

	snapshotsapi "github.com/containerd/containerd/v2/api/services/snapshots/v1"
	"github.com/containerd/containerd/v2/contrib/snapshotservice"
	"github.com/containerd/containerd/v2/plugins/snapshots/native"
)

func main() {
	// Provide a unix address to listen to, this will be the `address`
	// in the `proxy_plugin` configuration.
	// The root will be used to store the snapshots.
	if len(os.Args) < 3 {
		fmt.Printf("invalid args: usage: %s <unix addr> <root>\n", os.Args[0])
		os.Exit(1)
	}

	// Create a gRPC server
	rpc := grpc.NewServer()

	// Configure your custom snapshotter, this example uses the native
	// snapshotter and a root directory. Your custom snapshotter will be
	// much more useful than using a snapshotter which is already included.
	// https://godoc.org/github.com/containerd/containerd/snapshots#Snapshotter
	sn, err := native.NewSnapshotter(os.Args[2])
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}

	// Convert the snapshotter to a gRPC service,
	// example in github.com/containerd/containerd/contrib/snapshotservice
	service := snapshotservice.FromSnapshotter(sn)

	// Register the service with the gRPC server
	snapshotsapi.RegisterSnapshotsServer(rpc, service)

	// Listen and serve
	l, err := net.Listen("unix", os.Args[1])
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
	if err := rpc.Serve(l); err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
}
```

Using the previous configuration and example, you could run a snapshot plugin
with
```
# Start plugin in one terminal
$ go run ./main.go /var/run/mysnapshotter.sock /tmp/snapshots

# Use ctr in another
$ CONTAINERD_SNAPSHOTTER=customsnapshot ctr images pull docker.io/library/alpine:latest
$ tree -L 3 /tmp/snapshots
/tmp/snapshots
|-- metadata.db
`-- snapshots
    `-- 1
        |-- bin
        |-- dev
        |-- etc
        |-- home
        |-- lib
        |-- media
        |-- mnt
        |-- proc
        |-- root
        |-- run
        |-- sbin
        |-- srv
        |-- sys
        |-- tmp
        |-- usr
        `-- var

18 directories, 1 file
```

## Built-in Plugins

containerd uses plugins internally to ensure that internal implementations are
decoupled, stable, and treated equally with external plugins. To see all the
plugins containerd has, use `ctr plugins ls`

```
$ ctr plugins ls
TYPE                            ID                    PLATFORMS      STATUS
io.containerd.content.v1        content               -              ok
io.containerd.snapshotter.v1    btrfs                 linux/amd64    ok
io.containerd.snapshotter.v1    aufs                  linux/amd64    error
io.containerd.snapshotter.v1    native                linux/amd64    ok
io.containerd.snapshotter.v1    overlayfs             linux/amd64    ok
io.containerd.snapshotter.v1    zfs                   linux/amd64    error
io.containerd.metadata.v1       bolt                  -              ok
io.containerd.differ.v1         walking               linux/amd64    ok
io.containerd.gc.v1             scheduler             -              ok
io.containerd.service.v1        containers-service    -              ok
io.containerd.service.v1        content-service       -              ok
io.containerd.service.v1        diff-service          -              ok
io.containerd.service.v1        images-service        -              ok
io.containerd.service.v1        leases-service        -              ok
io.containerd.service.v1        namespaces-service    -              ok
io.containerd.service.v1        snapshots-service     -              ok
io.containerd.runtime.v1        linux                 linux/amd64    ok
io.containerd.runtime.v2        task                  linux/amd64    ok
io.containerd.monitor.v1        cgroups               linux/amd64    ok
io.containerd.service.v1        tasks-service         -              ok
io.containerd.internal.v1       restart               -              ok
io.containerd.grpc.v1           containers            -              ok
io.containerd.grpc.v1           content               -              ok
io.containerd.grpc.v1           diff                  -              ok
io.containerd.grpc.v1           events                -              ok
io.containerd.grpc.v1           healthcheck           -              ok
io.containerd.grpc.v1           images                -              ok
io.containerd.grpc.v1           leases                -              ok
io.containerd.grpc.v1           namespaces            -              ok
io.containerd.grpc.v1           snapshots             -              ok
io.containerd.grpc.v1           tasks                 -              ok
io.containerd.grpc.v1           version               -              ok
io.containerd.grpc.v1           cri                   linux/amd64    ok
```

From the output all the plugins can be seen as well those which did not
successfully load. In this case `aufs` and `zfs` are expected not to load
since they are not support on the machine. The logs will show why it failed,
but you can also get more details using the `-d` option.

```
$ ctr plugins ls -d id==aufs id==zfs
Type:          io.containerd.snapshotter.v1
ID:            aufs
Platforms:     linux/amd64
Exports:
               root      /var/lib/containerd/io.containerd.snapshotter.v1.aufs
Error:
               Code:        Unknown
               Message:     modprobe aufs failed: "modprobe: FATAL: Module aufs not found in directory /lib/modules/4.17.2-1-ARCH\n": exit status 1

Type:          io.containerd.snapshotter.v1
ID:            zfs
Platforms:     linux/amd64
Exports:
               root      /var/lib/containerd/io.containerd.snapshotter.v1.zfs
Error:
               Code:        Unknown
               Message:     path /var/lib/containerd/io.containerd.snapshotter.v1.zfs must be a zfs filesystem to be used with the zfs snapshotter
```

The error message which the plugin returned explains why the plugin was unable
to load.

#### Configuration

Plugins are configured using the `[plugins]` section of containerd's config.
Every plugin can have its own section using the pattern `[plugins."<plugin type>.<plugin id>"]`.

example configuration
```toml
version = 2

[plugins]
  [plugins."io.containerd.monitor.v1.cgroups"]
    no_prometheus = false
```

To see full configuration example run `containerd config default`.
If you want to get the configuration combined with your configuration, run `containerd config dump`.

##### Version header

containerd has several configuration versions:
- Version 3 (Recommended for containerd 2.x): Introduced in containerd 2.0.
  Several plugin IDs have changed in this version.
- Version 2 (Recommended for containerd 1.x): Introduced in containerd 1.3.
  Still supported in containerd v2.x.
  Plugin IDs are changed to have prefixes like "io.containerd.".
- Version 1: Introduced in containerd 1.0. Removed in containerd 2.0.

A configuration for Version 2 or 3 must specify the version `version = 2` or `version = 3` in the header, and must have
fully qualified plugin IDs in the `[plugins]` section:
```toml
version = 3

[plugins]
  [plugins.'io.containerd.monitor.task.v1.cgroups']
    no_prometheus = false
```

```toml
version = 2

[plugins]
  [plugins."io.containerd.monitor.v1.cgroups"]
    no_prometheus = false
```

A configuration with Version 1 may not have `version` header, and does not need fully qualified plugin IDs.
```toml
[plugins]
  [plugins.cgroups]
    no_prometheus = false
```