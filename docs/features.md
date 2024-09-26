
# Features

The sections below cover core features of `containerd`.

## Client

containerd offers a full client package to help you integrate containerd into your platform.

```go

import (
  "context"

  containerd "github.com/containerd/containerd/v2/client"
  "github.com/containerd/containerd/v2/pkg/cio"
  "github.com/containerd/containerd/v2/pkg/namespaces"
)


func main() {
	client, err := containerd.New("/run/containerd/containerd.sock")
	defer client.Close()
}

```

## Namespaces

Namespaces allow multiple consumers to use the same containerd without conflicting with each other.  It has the benefit of sharing content while maintaining separation with containers and images.

To set a namespace for requests to the API:

```go
context = context.Background()
// create a context for docker
docker = namespaces.WithNamespace(context, "docker")

containerd, err := client.NewContainer(docker, "id")
```

To set a default namespace on the client:

```go
client, err := containerd.New(address, containerd.WithDefaultNamespace("docker"))
```

## Distribution

```go
// pull an image
image, err := client.Pull(context, "docker.io/library/redis:latest")

// push an image
err := client.Push(context, "docker.io/library/redis:latest", image.Target())
```

## Containers

In containerd, a container is a metadata object. Resources such as an OCI runtime specification, image, root filesystem, and other metadata can be attached to a container.

```go
redis, err := client.NewContainer(context, "redis-master")
defer redis.Delete(context)
```

## OCI Runtime Specification

containerd fully supports the OCI runtime specification for running containers.  We have built-in functions to help you generate runtime specifications based on images as well as custom parameters.

You can specify options when creating a container about how to modify the specification.

```go
redis, err := client.NewContainer(context, "redis-master", containerd.WithNewSpec(oci.WithImageConfig(image)))
```

## Root Filesystems

containerd allows you to use overlay or snapshot filesystems with your containers.  It comes with built-in support for overlayfs and btrfs.

```go
// pull an image and unpack it into the configured snapshotter
image, err := client.Pull(context, "docker.io/library/redis:latest", containerd.WithPullUnpack)

// allocate a new RW root filesystem for a container based on the image
redis, err := client.NewContainer(context, "redis-master",
	containerd.WithNewSnapshot("redis-rootfs", image),
	containerd.WithNewSpec(oci.WithImageConfig(image)),
)

// use a readonly filesystem with multiple containers
for i := 0; i < 10; i++ {
	id := fmt.Sprintf("id-%s", i)
	container, err := client.NewContainer(ctx, id,
		containerd.WithNewSnapshotView(id, image),
		containerd.WithNewSpec(oci.WithImageConfig(image)),
	)
}
```

## Tasks

Taking a container object and turning it into a runnable process on a system is done by creating a new `Task` from the container.  A task represents the runnable object within containerd.

```go
// create a new task
task, err := redis.NewTask(context, cio.NewCreator(cio.WithStdio))
defer task.Delete(context)

// the task is now running and has a pid that can be used to setup networking
// or other runtime settings outside of containerd
pid := task.Pid()

// start the redis-server process inside the container
err := task.Start(context)

// wait for the task to exit and get the exit status
status, err := task.Wait(context)
```

## Checkpoint and Restore

If you have [criu](https://criu.org/Main_Page) installed on your machine you can checkpoint and restore containers and their tasks.  This allows you to clone and/or live migrate containers to other machines.

```go
// checkpoint the task then push it to a registry
checkpoint, err := task.Checkpoint(context)

err := client.Push(context, "myregistry/checkpoints/redis:master", checkpoint)

// on a new machine pull the checkpoint and restore the redis container
checkpoint, err := client.Pull(context, "myregistry/checkpoints/redis:master")

redis, err = client.NewContainer(context, "redis-master", containerd.WithNewSnapshot("redis-rootfs", checkpoint))
defer container.Delete(context)

task, err = redis.NewTask(context, cio.NewCreator(cio.WithStdio), containerd.WithTaskCheckpoint(checkpoint))
defer task.Delete(context)

err := task.Start(context)
```

## Snapshot Plugins

In addition to the built-in Snapshot plugins in containerd, additional external
plugins can be configured using GRPC. An external plugin is made available using
the configured name and appears as a plugin alongside the built-in ones.

To add an external snapshot plugin, add the plugin to containerd's config file
(by default at `/etc/containerd/config.toml`). The string following
`proxy_plugin.` will be used as the name of the snapshotter and the address
should refer to a socket with a GRPC listener serving containerd's Snapshot
GRPC API. Remember to restart containerd for any configuration changes to take
effect.

```
[proxy_plugins]
  [proxy_plugins.customsnapshot]
    type = "snapshot"
    address =  "/var/run/mysnapshotter.sock"
```

See [PLUGINS.md](PLUGINS.md) for how to create plugins
