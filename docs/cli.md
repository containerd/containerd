# Client CLI

There is a default cli named `ctr` based on the gRPC api. This cli will allow you to create and manage containers run with containerd.

The `ctr` cli is part of [containerd releases](https://github.com/containerd/containerd/releases), you can directly download the compiled version for your platform or you can build it youself

```sh
go build -o ctr cmd/ctr/main.go
```

## Usage

```sh
$ ctr
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
   1.6.0-beta.0+unknown

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

## Pulling images

```sh
$ ctr image pull --help
NAME:
   ctr images pull - pull an image from a remote

USAGE:
   ctr images pull [command options] [flags] <ref>

DESCRIPTION:
   Fetch and prepare an image for use in containerd.

After pulling an image, it should be ready to use the same reference in a run
command. As part of this process, we do the following:

1. Fetch all resources into containerd.
2. Prepare the snapshot filesystem with the pulled resources.
3. Register metadata for the image.


OPTIONS:
   --skip-verify, -k                 skip SSL certificate validation
   --plain-http                      allow connections using plain HTTP
   --user value, -u value            user[:password] Registry user and password
   --refresh value                   refresh token for authorization server
   --hosts-dir value                 Custom hosts configuration directory
   --tlscacert value                 path to TLS root CA
   --tlscert value                   path to TLS client certificate
   --tlskey value                    path to TLS client key
   --http-dump                       dump all HTTP request/responses when interacting with container registry
   --http-trace                      enable HTTP tracing for registry interactions
   --snapshotter value               snapshotter name. Empty value stands for the default value. [$CONTAINERD_SNAPSHOTTER]
   --label value                     labels to attach to the image
   --platform value                  Pull content from a specific platform
   --all-platforms                   pull content and metadata from all platforms
   --all-metadata                    Pull metadata for all platforms
   --print-chainid                   Print the resulting image's chain ID
   --max-concurrent-downloads value  Set the max concurrent downloads for each pull (default: 0)
```

example:

```sh
$ ctr image pull docker.io/library/alpine:latest
docker.io/library/alpine:latest:                                                  resolved       |++++++++++++++++++++++++++++++++++++++|
index-sha256:e1c082e3d3c45cccac829840a25941e679c25d438cc8412c2fa221cf1a824e6a:    done           |++++++++++++++++++++++++++++++++++++++|
manifest-sha256:69704ef328d05a9f806b6b8502915e6a0a4faa4d72018dc42343f511490daf8a: done           |++++++++++++++++++++++++++++++++++++++|
layer-sha256:a0d0a0d46f8b52473982a3c466318f479767577551a53ffc9074c9fa7035982e:    done           |++++++++++++++++++++++++++++++++++++++|
config-sha256:14119a10abf4669e8cdbdff324a9f9605d99697215a0d21c360fe8dfa8471bab:   done           |++++++++++++++++++++++++++++++++++++++|
elapsed: 4.6 s                                                                    total:  2.0 Mi (445.9 KiB/s)
unpacking linux/amd64 sha256:e1c082e3d3c45cccac829840a25941e679c25d438cc8412c2fa221cf1a824e6a...
done: 156.95631ms
```

## Listing images

```sh
$ ctr image ls --help
NAME:
   ctr images list - list images known to containerd

USAGE:
   ctr images list [command options] [flags] [<filter>, ...]

DESCRIPTION:
   list images registered with containerd

OPTIONS:
   --quiet, -q  print only the image refs
```

example:

```sh
$ ctr image ls
REF                             TYPE                                                      DIGEST                                                                  SIZE     PLATFORMS                                                                                LABELS
docker.io/library/alpine:latest application/vnd.docker.distribution.manifest.list.v2+json sha256:e1c082e3d3c45cccac829840a25941e679c25d438cc8412c2fa221cf1a824e6a 2.7 MiB  linux/386,linux/amd64,linux/arm/v6,linux/arm/v7,linux/arm64/v8,linux/ppc64le,linux/s390x -
```

## Create a container

```sh
$ ctr container create --help
NAME:
   ctr containers create - create container

USAGE:
   ctr containers create [command options] [flags] Image|RootFS CONTAINER [COMMAND] [ARG...]

OPTIONS:
   --snapshotter value               snapshotter name. Empty value stands for the default value. [$CONTAINERD_SNAPSHOTTER]
   --snapshotter-label value         labels added to the new snapshot for this container.
   --config value, -c value          path to the runtime-specific spec config file
   --cwd value                       specify the working directory of the process
   --env value                       specify additional container environment variables (e.g. FOO=bar)
   --env-file value                  specify additional container environment variables in a file(e.g. FOO=bar, one per line)
   --label value                     specify additional labels (e.g. foo=bar)
   --mount value                     specify additional container mount (e.g. type=bind,src=/tmp,dst=/host,options=rbind:ro)
   --net-host                        enable host networking for the container
   --privileged                      run privileged container
   --read-only                       set the containers filesystem as readonly
   --runtime value                   runtime name (default: "io.containerd.runc.v2")
   --runtime-config-path value       optional runtime config path
   --tty, -t                         allocate a TTY for the container
   --with-ns value                   specify existing Linux namespaces to join at container runtime (format '<nstype>:<path>')
   --pid-file value                  file path to write the task's pid
   --gpus value                      add gpus to the container
   --allow-new-privs                 turn off OCI spec's NoNewPrivileges feature flag
   --memory-limit value              memory limit (in bytes) for the container (default: 0)
   --device value                    file path to a device to add to the container; or a path to a directory tree of devices to add to the container
   --seccomp                         enable the default seccomp profile
   --seccomp-profile value           file path to custom seccomp profile. seccomp must be set to true, before using seccomp-profile
   --apparmor-default-profile value  enable AppArmor with the default profile with the specified name, e.g. "cri-containerd.apparmor.d"
   --apparmor-profile value          enable AppArmor with an existing custom profile
   --rootfs                          use custom rootfs that is not managed by containerd snapshotter
   --no-pivot                        disable use of pivot-root (linux only)
   --cpu-quota value                 Limit CPU CFS quota (default: -1)
   --cpu-period value                Limit CPU CFS period (default: 0)
   --rootfs-propagation value        set the propagation of the container rootfs
```

example:

```sh
$ ctr container create docker.io/library/alpine:latest 1
```

## List containers

```sh
$ ctr containers ls --help
NAME:
   ctr containers list - list containers

USAGE:
   ctr containers list [command options] [flags] [<filter>, ...]

OPTIONS:
   --quiet, -q  print only the container id
```

example:

```sh
$ ctr container ls
CONTAINER    IMAGE                              RUNTIME
1            docker.io/library/alpine:latest    io.containerd.runc.v2
```

## Start a container

```sh
$ ctr task start --help
NAME:
   ctr tasks start - start a container that has been created

USAGE:
   ctr tasks start [command options] CONTAINER

OPTIONS:
   --null-io         send all IO to /dev/null
   --log-uri value   log uri
   --fifo-dir value  directory used for storing IO FIFOs
   --pid-file value  file path to write the task's pid
   --detach, -d      detach from the task after it has started execution
```

example:

```sh
$ ctr tasks start 1
```

## Execute a process in a running container

```sh
$ ctr task exec --help
NAME:
   ctr tasks exec - execute additional processes in an existing container

USAGE:
   ctr tasks exec [command options] [flags] CONTAINER CMD [ARG...]

OPTIONS:
   --cwd value       working directory of the new process
   --tty, -t         allocate a TTY for the container
   --detach, -d      detach from the task after it has started execution
   --exec-id value   exec specific id for the process
   --fifo-dir value  directory used for storing IO FIFOs
   --log-uri value   log uri for custom shim logging
   --user value      user id or name
```

example:

```sh
$ ctr task exec --exec-id 1 1 cat /etc/alpine-release
3.14.2
```

 ## Kill a container's process

```sh
$ ctr task kill --help
NAME:
   ctr tasks kill - signal a container (default: SIGTERM)

USAGE:
   ctr tasks kill [command options] [flags] CONTAINER

OPTIONS:
   --signal value, -s value  signal to send to the container
   --exec-id value           process ID to kill
   --all, -a                 send signal to all processes inside the container
```

example:

```sh
 $ ctr task kill -s 9 1
```