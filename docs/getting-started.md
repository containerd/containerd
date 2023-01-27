# Getting started with containerd

## Installing containerd

### Option 1: From the official binaries

The official binary releases of containerd are available for the `amd64` (also known as `x86_64`) and `arm64` (also known as `aarch64`) architectures.

Typically, you will have to install [runc](https://github.com/opencontainers/runc/releases) and [CNI plugins](https://github.com/containernetworking/plugins/releases)
from their official sites too.

#### Step 1: Installing containerd

Download the `containerd-<VERSION>-<OS>-<ARCH>.tar.gz` archive from https://github.com/containerd/containerd/releases ,
verify its sha256sum, and extract it under `/usr/local`:

```console
$ tar Cxzvf /usr/local containerd-1.6.2-linux-amd64.tar.gz
bin/
bin/containerd-shim-runc-v2
bin/containerd-shim
bin/ctr
bin/containerd-shim-runc-v1
bin/containerd
bin/containerd-stress
```

The `containerd` binary is built dynamically for glibc-based Linux distributions such as Ubuntu and Rocky Linux.
This binary may not work on musl-based distributions such as Alpine Linux.
Users of such distributions may have to install containerd from the source or a third party package.

> **FAQ**: For Kubernetes, do I need to download `cri-containerd-(cni-)<VERSION>-<OS-<ARCH>.tar.gz` too?
>
> **Answer**: No.
>
> As the Kubernetes CRI feature has been already included in `containerd-<VERSION>-<OS>-<ARCH>.tar.gz`,
> you do not need to download the `cri-containerd-....` archives to use CRI.
>
> The `cri-containerd-...` archives are [deprecated](https://github.com/containerd/containerd/blob/main/RELEASES.md#deprecated-features),
> do not work on old Linux distributions, and will be removed in containerd 2.0.


##### systemd
If you intend to start containerd via systemd, you should also download the `containerd.service` unit file from
https://raw.githubusercontent.com/containerd/containerd/main/containerd.service into `/usr/local/lib/systemd/system/containerd.service`,
and run the following commands:

```bash
systemctl daemon-reload
systemctl enable --now containerd
```

#### Step 2: Installing runc

Download the `runc.<ARCH>` binary from https://github.com/opencontainers/runc/releases ,
verify its sha256sum, and install it as `/usr/local/sbin/runc`.

```console
$ install -m 755 runc.amd64 /usr/local/sbin/runc
```

The binary is built statically and should work on any Linux distribution.

#### Step 3: Installing CNI plugins

Download the `cni-plugins-<OS>-<ARCH>-<VERSION>.tgz` archive from https://github.com/containernetworking/plugins/releases ,
verify its sha256sum, and extract it under `/opt/cni/bin`:

```console
$ mkdir -p /opt/cni/bin
$ tar Cxzvf /opt/cni/bin cni-plugins-linux-amd64-v1.1.1.tgz
./
./macvlan
./static
./vlan
./portmap
./host-local
./vrf
./bridge
./tuning
./firewall
./host-device
./sbr
./loopback
./dhcp
./ptp
./ipvlan
./bandwidth
```

The binaries are built statically and should work on any Linux distribution.

### Option 2: From `apt-get` or `dnf`

The `containerd.io` packages in DEB and RPM formats are distributed by Docker (not by the containerd project).
See the Docker documentation for how to set up `apt-get` or `dnf` to install `containerd.io` packages:
- [CentOS](https://docs.docker.com/engine/install/centos/)
- [Debian](https://docs.docker.com/engine/install/debian/)
- [Fedora](https://docs.docker.com/engine/install/fedora/)
- [Ubuntu](https://docs.docker.com/engine/install/ubuntu/)

The `containerd.io` package contains runc too, but does not contain CNI plugins.

### Option 3: From source

To install containerd and its dependencies from the source, see [`BUILDING.md`](/BUILDING.md).

## Installing containerd on Windows

From a PowerShell session run the following commands:

```PowerShell
# Download and extract desired containerd Windows binaries
$Version="1.6.4"
curl.exe -L https://github.com/containerd/containerd/releases/download/v$Version/containerd-$Version-windows-amd64.tar.gz -o containerd-windows-amd64.tar.gz
tar.exe xvf .\containerd-windows-amd64.tar.gz

# Copy and configure
Copy-Item -Path ".\bin\*" -Destination "$Env:ProgramFiles\containerd" -Recurse -Force
cd $Env:ProgramFiles\containerd\
.\containerd.exe config default | Out-File config.toml -Encoding ascii

# Review the configuration. Depending on setup you may want to adjust:
# - the sandbox_image (Kubernetes pause image)
# - cni bin_dir and conf_dir locations
Get-Content config.toml

# Register and start service
.\containerd.exe --register-service
Start-Service containerd
```

## Interacting with containerd via CLI

There are several command line interface (CLI) projects for interacting with containerd:

Name      | Community             | API    | Target             | Web site                                    |
----------|-----------------------|------- | -------------------|---------------------------------------------|
`ctr`     | containerd            | Native | For debugging only | (None, see `ctr --help` to learn the usage) |
`nerdctl` | containerd (non-core) | Native | General-purpose    | https://github.com/containerd/nerdctl       |
`crictl`  | Kubernetes SIG-node   | CRI    | For debugging only | https://github.com/kubernetes-sigs/cri-tools/blob/master/docs/crictl.md |

While the `ctr` tool is bundled together with containerd, it should be noted the `ctr` tool is solely made for debugging containerd.
The [`nerdctl`](https://github.com/containerd/nerdctl) tool provides stable and human-friendly user experience.

Example (`ctr`):
```bash
ctr images pull docker.io/library/redis:alpine
ctr run docker.io/library/redis:alpine redis
```

Example (`nerdctl`):
```bash
nerdctl run --name redis redis:alpine
```

## Setting up containerd for Kubernetes

containerd has built-in support for Kubernetes Container Runtime Interface (CRI).

To set up containerd nodes for managed Kubernetes services, see the service providers' documentations:
- [Amazon Elastic Kubernetes Service](https://docs.aws.amazon.com/eks/latest/userguide/dockershim-deprecation.html)
- [Azure Kubernetes Service](https://docs.microsoft.com/en-us/azure/aks/cluster-configuration)
- [Google Kubernetes Engine](https://cloud.google.com/kubernetes-engine/docs/concepts/using-containerd)

For non-managed environments, see the following Kubernetes documentations:
- [Getting started / Production environment / Container runtimes](https://kubernetes.io/docs/setup/production-environment/container-runtimes/)
- [Getting started / Production environment / Installing Kubernetes with deployment tools](https://kubernetes.io/docs/setup/production-environment/tools/)

- - -

# Advanced topics

## Customizing containerd

containerd uses a configuration file located in `/etc/containerd/config.toml` for specifying daemon level options.
A sample configuration file can be found [here](/docs/man/containerd-config.toml.5.md).

The default configuration can be generated via `containerd config default > /etc/containerd/config.toml`.

## Implementing your own containerd client
There are many different ways to use containerd.
If you are a developer working on containerd you can use the `ctr` tool or the `nerdctl` tool to quickly test features and functionality without writing extra code.
However, if you want to integrate containerd into your project we have an easy to use client package that allows you to work with containerd.

In this guide we will pull and run a redis server with containerd using the client package.
This project requires a recent version of Go.
See the header of [`go.mod`](https://github.com/containerd/containerd/blob/main/go.mod) for the recommended Go version.

### Connecting to containerd

We will start a new `main.go` file and import the containerd root package that contains the client.


```go
package main

import (
	"log"

	"github.com/containerd/containerd"
)

func main() {
	if err := redisExample(); err != nil {
		log.Fatal(err)
	}
}

func redisExample() error {
	client, err := containerd.New("/run/containerd/containerd.sock")
	if err != nil {
		return err
	}
	defer client.Close()
	return nil
}
```

This will create a new client with the default containerd socket path.
Because we are working with a daemon over GRPC we need to create a `context` for use with calls to client methods.
containerd is also namespaced for callers of the API.
We should also set a namespace for our guide after creating the context.

```go
	ctx := namespaces.WithNamespace(context.Background(), "example")
```

Having a namespace for our usage ensures that containers, images, and other resources without containerd do not conflict with other users of a single daemon.

### Pulling the redis image

Now that we have a client to work with we need to pull an image.
We can use the redis image based on Alpine Linux from the DockerHub.

```go
	image, err := client.Pull(ctx, "docker.io/library/redis:alpine", containerd.WithPullUnpack)
	if err != nil {
		return err
	}
```

The containerd client uses the `Opts` pattern for many of the method calls.
We use the `containerd.WithPullUnpack` so that we not only fetch and download the content into containerd's content store but also unpack it into a snapshotter for use as a root filesystem.

Let's put the code together that will pull the redis image based on alpine linux from Dockerhub and then print the name of the image on the console's output.

```go
package main

import (
        "context"
        "log"

        "github.com/containerd/containerd"
        "github.com/containerd/containerd/namespaces"
)

func main() {
        if err := redisExample(); err != nil {
                log.Fatal(err)
        }
}

func redisExample() error {
        client, err := containerd.New("/run/containerd/containerd.sock")
        if err != nil {
                return err
        }
        defer client.Close()

        ctx := namespaces.WithNamespace(context.Background(), "example")
        image, err := client.Pull(ctx, "docker.io/library/redis:alpine", containerd.WithPullUnpack)
        if err != nil {
                return err
        }
        log.Printf("Successfully pulled %s image\n", image.Name())

        return nil
}
```

```bash
> go build main.go
> sudo ./main

2017/08/13 17:43:21 Successfully pulled docker.io/library/redis:alpine image
```

### Creating an OCI Spec and Container

Now that we have an image to base our container off of, we need to generate an OCI runtime specification that the container can be based off of as well as the new container.

containerd provides reasonable defaults for generating OCI runtime specs.
There is also an `Opt` for modifying the default config based on the image that we pulled.

The container will be based off of the image, and we will:
1. allocate a new read-write snapshot so the container can store any persistent information.
2. create a new spec for the container.


```go
	container, err := client.NewContainer(
		ctx,
		"redis-server",
		containerd.WithNewSnapshot("redis-server-snapshot", image),
		containerd.WithNewSpec(oci.WithImageConfig(image)),
	)
	if err != nil {
		return err
	}
	defer container.Delete(ctx, containerd.WithSnapshotCleanup)
```

If you have an existing OCI specification created you can use `containerd.WithSpec(spec)` to set it on the container.

When creating a new snapshot for the container we need to provide a snapshot ID as well as the Image that the container will be based on.
By providing a separate snapshot ID than the container ID we can easily reuse, existing snapshots across different containers.

We also add a line to delete the container along with its snapshot after we are done with this example.

Here is example code to pull the redis image based on alpine linux from Dockerhub, create an OCI spec, create a container based on the spec and finally delete the container.
```go
package main

import (
        "context"
        "log"

        "github.com/containerd/containerd"
        "github.com/containerd/containerd/oci"
        "github.com/containerd/containerd/namespaces"
)

func main() {
        if err := redisExample(); err != nil {
                log.Fatal(err)
        }
}

func redisExample() error {
        client, err := containerd.New("/run/containerd/containerd.sock")
        if err != nil {
                return err
        }
        defer client.Close()

        ctx := namespaces.WithNamespace(context.Background(), "example")
        image, err := client.Pull(ctx, "docker.io/library/redis:alpine", containerd.WithPullUnpack)
        if err != nil {
                return err
        }
        log.Printf("Successfully pulled %s image\n", image.Name())

        container, err := client.NewContainer(
                ctx,
                "redis-server",
                containerd.WithNewSnapshot("redis-server-snapshot", image),
                containerd.WithNewSpec(oci.WithImageConfig(image)),
        )
        if err != nil {
                return err
        }
        defer container.Delete(ctx, containerd.WithSnapshotCleanup)
        log.Printf("Successfully created container with ID %s and snapshot with ID redis-server-snapshot", container.ID())

        return nil
}
```

Let's see it in action.

```bash
> go build main.go
> sudo ./main

2017/08/13 18:01:35 Successfully pulled docker.io/library/redis:alpine image
2017/08/13 18:01:35 Successfully created container with ID redis-server and snapshot with ID redis-server-snapshot
```

### Creating a running Task

One thing that may be confusing at first for new containerd users is the separation between a `Container` and a `Task`.
A container is a metadata object that resources are allocated and attached to.
A task is a live, running process on the system.
Tasks should be deleted after each run while a container can be used, updated, and queried multiple times.

```go
	task, err := container.NewTask(ctx, cio.NewCreator(cio.WithStdio))
	if err != nil {
		return err
	}
	defer task.Delete(ctx)
```

The new task that we just created is actually a running process on your system.
We use `cio.WithStdio` so that all IO from the container is sent to our `main.go` process.
This is a `cio.Opt` that configures the `Streams` used by `NewCreator` to return a `cio.IO`
for the new task.

If you are familiar with the OCI runtime actions, the task is currently in the "created" state.
This means that the namespaces, root filesystem, and various container level settings have been initialized but the user defined process, in this example "redis-server", has not been started.
This gives users a chance to setup network interfaces or attach different tools to monitor the container.
containerd also takes this opportunity to monitor your container as well.
Waiting on things like the container's exit status and cgroup metrics are setup at this point.

If you are familiar with prometheus you can curl the containerd metrics endpoint (in the `config.toml` that we created) to see your container's metrics:

```bash
> curl 127.0.0.1:1338/v1/metrics
```

Pretty cool right?

### Task Wait and Start

Now that we have a task in the created state we need to make sure that we wait on the task to exit.
It is essential to wait for the task to finish so that we can close our example and cleanup the resources that we created.
You always want to make sure you `Wait` before calling `Start` on a task.
This makes sure that you do not encounter any races if the task has a simple program like `/bin/true` that exits promptly after calling start.

```go
	exitStatusC, err := task.Wait(ctx)
	if err != nil {
		return err
	}

	if err := task.Start(ctx); err != nil {
		return err
	}
```

Now we should see the `redis-server` logs in our terminal when we run the `main.go` file.

### Killing the task

Since we are running a long running server we will need to kill the task in order to exit out of our example.
To do this we will simply call `Kill` on the task after waiting a couple of seconds so we have a chance to see the redis-server logs.

```go
	time.Sleep(3 * time.Second)

	if err := task.Kill(ctx, syscall.SIGTERM); err != nil {
		return err
	}

	status := <-exitStatusC
	code, exitedAt, err := status.Result()
	if err != nil {
		return err
	}
	fmt.Printf("redis-server exited with status: %d\n", code)
```

We wait on our exit status channel that we setup to ensure the task has fully exited and we get the exit status.
If you have to reload containers or miss waiting on a task, `Delete` will also return the exit status when you finally delete the task.
We got you covered.

```go
status, err := task.Delete(ctx)
```

### Full Example

Here is the full example that we just put together.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/namespaces"
)

func main() {
	if err := redisExample(); err != nil {
		log.Fatal(err)
	}
}

func redisExample() error {
	// create a new client connected to the default socket path for containerd
	client, err := containerd.New("/run/containerd/containerd.sock")
	if err != nil {
		return err
	}
	defer client.Close()

	// create a new context with an "example" namespace
	ctx := namespaces.WithNamespace(context.Background(), "example")

	// pull the redis image from DockerHub
	image, err := client.Pull(ctx, "docker.io/library/redis:alpine", containerd.WithPullUnpack)
	if err != nil {
		return err
	}

	// create a container
	container, err := client.NewContainer(
		ctx,
		"redis-server",
		containerd.WithImage(image),
		containerd.WithNewSnapshot("redis-server-snapshot", image),
		containerd.WithNewSpec(oci.WithImageConfig(image)),
	)
	if err != nil {
		return err
	}
	defer container.Delete(ctx, containerd.WithSnapshotCleanup)

	// create a task from the container
	task, err := container.NewTask(ctx, cio.NewCreator(cio.WithStdio))
	if err != nil {
		return err
	}
	defer task.Delete(ctx)

	// make sure we wait before calling start
	exitStatusC, err := task.Wait(ctx)
	if err != nil {
		fmt.Println(err)
	}

	// call start on the task to execute the redis server
	if err := task.Start(ctx); err != nil {
		return err
	}

	// sleep for a lil bit to see the logs
	time.Sleep(3 * time.Second)

	// kill the process and get the exit status
	if err := task.Kill(ctx, syscall.SIGTERM); err != nil {
		return err
	}

	// wait for the process to fully exit and print out the exit status

	status := <-exitStatusC
	code, _, err := status.Result()
	if err != nil {
		return err
	}
	fmt.Printf("redis-server exited with status: %d\n", code)

	return nil
}
```

We can build this example and run it as follows to see our hard work come together.

```bash
> go build main.go
> sudo ./main

1:C 04 Aug 20:41:37.682 # oO0OoO0OoO0Oo Redis is starting oO0OoO0OoO0Oo
1:C 04 Aug 20:41:37.682 # Redis version=4.0.1, bits=64, commit=00000000, modified=0, pid=1, just started
1:C 04 Aug 20:41:37.682 # Warning: no config file specified, using the default config. In order to specify a config file use redis-server /path/to/redis.conf
1:M 04 Aug 20:41:37.682 # You requested maxclients of 10000 requiring at least 10032 max file descriptors.
1:M 04 Aug 20:41:37.682 # Server can't set maximum open files to 10032 because of OS error: Operation not permitted.
1:M 04 Aug 20:41:37.682 # Current maximum open files is 1024. maxclients has been reduced to 992 to compensate for low ulimit. If you need higher maxclients increase 'ulimit -n'.
1:M 04 Aug 20:41:37.683 * Running mode=standalone, port=6379.
1:M 04 Aug 20:41:37.683 # WARNING: The TCP backlog setting of 511 cannot be enforced because /proc/sys/net/core/somaxconn is set to the lower value of 128.
1:M 04 Aug 20:41:37.684 # Server initialized
1:M 04 Aug 20:41:37.684 # WARNING overcommit_memory is set to 0! Background save may fail under low memory condition. To fix this issue add 'vm.overcommit_memory = 1' to /etc/sysctl.conf and then reboot or run the command 'sysctl vm.overcommit_memory=1' for this to take effect.
1:M 04 Aug 20:41:37.684 # WARNING you have Transparent Huge Pages (THP) support enabled in your kernel. This will create latency and memory usage issues with Redis. To fix this issue run the command 'echo never > /sys/kernel/mm/transparent_hugepage/enabled' as root, and add it to your /etc/rc.local in order to retain the setting after a reboot. Redis must be restarted after THP is disabled.
1:M 04 Aug 20:41:37.684 * Ready to accept connections
1:signal-handler (1501879300) Received SIGTERM scheduling shutdown...
1:M 04 Aug 20:41:40.791 # User requested shutdown...
1:M 04 Aug 20:41:40.791 * Saving the final RDB snapshot before exiting.
1:M 04 Aug 20:41:40.794 * DB saved on disk
1:M 04 Aug 20:41:40.794 # Redis is now ready to exit, bye bye...
redis-server exited with status: 0
```

In the end, we really did not write that much code when you use the client package.

- - -
We hope this guide helped to get you up and running with containerd.
Feel free to join the `#containerd` and `#containerd-dev` slack channels on Cloud Native Computing Foundation's (CNCF) slack - `cloud-native.slack.com` if you have any questions and like all things, if you want to help contribute to containerd or this guide, submit a pull request. [Get Invite to CNCF slack.](https://slack.cncf.io)
