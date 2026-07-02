# Client CLI

The containerd provides a client called `ctr` to interact with the [`gRPC`](https://grpc.io)
API to create and manage containers run with the containerd runtime. The intent
of this utility is primarily for troubleshooting and inspecting the containerd
daemon and **NOT** for production use as it does not guarantee a stable interface.

```bash
$ ctr --help
NAME:
   ctr -
        __
  _____/ /______
 / ___/ __/ ___/
/ /__/ /_/ /
\___/\__/_/

containerd CLI


USAGE:
   ctr [global options] command [command options] [arguments...]

VERSION:
   v1.5.0-595-g193bafc42

DESCRIPTION:

ctr is an unsupported debug and administrative client for interacting
with the containerd daemon. Because it is unsupported, the commands,
options, and operations are not guaranteed to be backward compatible or
stable from release to release of the containerd project.

COMMANDS:
   plugins, plugin            provides information about containerd plugins
   version                    print the client and server versions
   containers, c, container   manage containers
   content                    manage content
   events, event              display containerd events
   images, image, i           manage images
   leases                     manage leases
   namespaces, namespace, ns  manage namespaces
   pprof                      provide golang pprof outputs for containerd
   run                        run a container
   snapshots, snapshot        manage snapshots
   tasks, t, task             manage tasks
   install                    install a new package
   oci                        OCI tools
   shim                       interact with a shim directly
   help, h                    Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --debug                      enable debug output in logs
   --address value, -a value    address for containerd's GRPC server (default: "/run/containerd/containerd.sock") [$CONTAINERD_ADDRESS]
   --timeout value              total timeout for ctr commands (default: 0s)
   --connect-timeout value      timeout for connecting to containerd (default: 0s)
   --namespace value, -n value  namespace to use with commands (default: "default") [$CONTAINERD_NAMESPACE]
   --help, -h                   show help
   --version, -v                print the version
```

## Get the containerd version
```bash
$ sudo ctr version
Client:
  Version:  1.5.5-0ubuntu3~20.04.1
  Revision:
  Go version: go1.13.8

Server:
  Version:  1.5.5-0ubuntu3~20.04.1
  Revision:
  UUID: dfe9c2c3-cdf3-4fae-8e54-8eca8ab5ce20
```

## Creating namespaces
```bash
$ sudo ctr namespace create test foo=bar
$ sudo ctr namespace list
NAME    LABELS
default
test    foo=bar
```
Labels can be supplied after the name of the namespace.

## Pulling containers

`ctr` can be used to pull containers from container repositories. For example,
the alpine image is pulled from the docker repository.
```bash
$ sudo ctr image pull docker.io/library/alpine:latest
```

## Listing containers

The pulled images can be listed as
```bash
$ sudo ctr image list
REF                             TYPE                                                      DIGEST                                                                  SIZE    PLATFORMS                                                                                LABELS
docker.io/library/alpine:latest application/vnd.docker.distribution.manifest.list.v2+json sha256:e1c082e3d3c45cccac829840a25941e679c25d438cc8412c2fa221cf1a824e6a 2.7 MiB linux/386,linux/amd64,linux/arm/v6,linux/arm/v7,linux/arm64/v8,linux/ppc64le,linux/s390x -
```

## Interacting with containers

```bash
$ ctr containers --help
NAME:
   ctr containers - manage containers

USAGE:
   ctr containers command [command options] [arguments...]

COMMANDS:
   create           create container
   delete, del, rm  delete one or more existing containers
   info             get info about a container
   list, ls         list containers
   label            set and clear labels for a container
   checkpoint       checkpoint a container
   restore          restore a container from checkpoint

OPTIONS:
   --help, -h  show help
```

A container can be created using the pulled container image or an [OCI bundle](docs/bundle.md).

### Using the pulled image
```bash
$ sudo ctr containers create docker.io/library/alpine:latest alpine1
```

## Running a container
```bash
$ sudo ctr run --rm -t docker.io/library/alpine:latest alpine2 sh
/ # ps -eaf
PID   USER     TIME  COMMAND
    1 root      0:00 sh
    8 root      0:00 ps -eaf

# For more help and options
$ sudo ctr run --help
```

Note that the `-t` flag is to assign a tty to the shell, or use `-d` to detach
the container.

## Listing containers
The running containers can be listed using the list command.
```bash
$ sudo ctr containers list
CONTAINER    IMAGE                              RUNTIME
alpine1      docker.io/library/alpine:latest    io.containerd.runc.v2
alpine2      docker.io/library/alpine:latest    io.containerd.runc.v2
```

## Managing tasks

Create a container, for example in the following way,
```bash
$ sudo ctr run --rm -t  docker.io/library/alpine:latest alpine3 sh
/ # ps -eaf
PID   USER     TIME  COMMAND
    1 root      0:00 sh
    8 root      0:00 ps -eaf
```
Once this is running, we can check the tasks as follows,
```bash
$ sudo ctr task list
TASK       PID     STATUS
alpine3    6956    RUNNING
```

The PID is from the root namespace and can be checked in the host machine.

To manage the tasks, run something, eg `sleep 100` in the shell spawned from the
first command that was creating a container.
```bash
$ sudo ctr task ps alpine3
PID     INFO
6956    -
7001    -
```

To execute a command in a container,
```bash
$ ctr task exec --exec-id 100 alpine3 ps -eaf
PID   USER     TIME  COMMAND
    1 root      0:00 sh
    6 root      0:00 sleep 10000
    7 root      0:00 ps -eaf
```

To kill all the tasks,
```bash
$ sudo ctr task kill --signal 9 --all alpine3
```

To attach to the stdout of the container, the following options can be used
```bash
$ sudo ctr task attach alpine3
```
This will be attaching the new process to the STDIN and STDOUT of the container.

## Deleting a container
`ctr` can be used to delete a running container using the container id.
```bash
$ sudo ctr containers delete --help
NAME:
   ctr containers delete - delete one or more existing containers

USAGE:
   ctr containers delete [command options] [flags] CONTAINER [CONTAINER, ...]

OPTIONS:
   --keep-snapshot  do not clean up snapshot with container
```

Create a container,
```bash
$ sudo ctr run -d docker.io/library/redis:alpine redis3
```
To delete a container, make sure it's stopped first, i.e. all tasks are stopped.
If not done so, the following error may come up.
```
ERRO[0000] failed to delete container "redis3"           error="cannot delete a non stopped container: {running 0 0001-01-01 00:00:00 +0000 UTC}"
ctr: cannot delete a non stopped container: {running 0 0001-01-01 00:00:00 +0000 UTC}
```
To forcefully kill all tasks and then delete the container,
```bash
$ sudo ctr task kill --all --signal 9 redis3
$ sudo ctr containers delete redis3
```

In case the container is created using
``` bash
$ sudo ctr containers create docker.io/library/redis:alpine redis4
```
It can be deleted using, `ctr containers delete redis4`.

## Creating a container checkpoint
```bash
$ sudo ctr run -d docker.io/library/redis:alpine redis5
$ sudo ctr containers checkpoint --rw --task redis5 checkpoint/redis5-01
```
Once the checkpoint is created, it can be listed (along with other images)
```bash
$ sudo ctr image ls
REF                             TYPE                                                      DIGEST                                                                  SIZE     PLATFORMS                                                                                LABELS
checkpoint/redis5-01            application/vnd.oci.image.index.v1+json                   sha256:582e363b9e804a8015929ea4ed8ac7d683a1d605dd03ff9e1ae78dfd904be529 1.3 KiB  linux/amd64                                                                              -
docker.io/library/alpine:latest application/vnd.docker.distribution.manifest.list.v2+json sha256:e1c082e3d3c45cccac829840a25941e679c25d438cc8412c2fa221cf1a824e6a 2.7 MiB  linux/386,linux/amd64,linux/arm/v6,linux/arm/v7,linux/arm64/v8,linux/ppc64le,linux/s390x -
docker.io/library/redis:alpine  application/vnd.docker.distribution.manifest.list.v2+json sha256:58132ff3162cf9ecc8e2042c77b2ec46f6024c35e83bda3cabde76437406f8ac 10.4 MiB linux/386,linux/amd64,linux/arm/v6,linux/arm/v7,linux/arm64/v8,linux/ppc64le,linux/s390x -
```

### Restoring the container checkpoint

Once the checkpoint is created, it's available in the image list, it can be
restored as

```bash
$ sudo ctr restore redis-debug checkpoint/redis5-01
```
Or, restore in the live mode
```bash
$ sudo ctr restore --live redis-debug checkpoint/redis5-01
```

> **Note**: The container checkpoint image may or may not include live container
> state (as expected for [`CRIU`](https://github.com/checkpoint-restore/criu)
> image). It is expected to only have configuration and file system checkpoints.
