# Runtime v2

Runtime v2 introduces a first class shim API for runtime authors to integrate with containerd.
The shim API is minimal and scoped to the execution lifecycle of a container.

## Binary Naming

Users specify the runtime they wish to use when creating a container.
The runtime can also be changed via a container update.

```bash
> ctr run --runtime io.containerd.runc.v1
```

When a user specifies a runtime name, `io.containerd.runc.v1`, they will specify the name and version of the runtime.
This will be translated by containerd into a binary name for the shim.

`io.containerd.runc.v1` -> `containerd-shim-runc-v1`

containerd keeps the `containerd-shim-*` prefix so that users can `ps aux | grep containerd-shim` to see running shims on their system.

## Shim Authoring

This section is dedicated to runtime authors wishing to build a shim.
It will detail how the API works and different considerations when building shim.

### Commands

Container information is provided to a shim in two ways.
The OCI Runtime Bundle and on the `Create` rpc request.

#### `start`

Each shim MUST implement a `start` subcommand.
This command will launch new shims.
The start command MUST accept the following flags:

* `-namespace` the namespace for the container
* `-id` the id of the container
* `-address` the address of the containerd's main socket
* `-publish-binary` the binary path to publish events back to containerd

The start command, as well as all binary calls to the shim, has the bundle for the container set as the `cwd`.

The start command MUST return an address to a shim for containerd to issue API requests for container operations.

The start command can either start a new shim or return an address to an existing shim based on the shim's logic.

#### `delete`

Each shim MUST implement a `delete` subcommand.
This command allows containerd to delete any container resources created, mounted, and/or run by a shim when containerd can no longer communicate over rpc.
This happens if a shim is SIGKILL'd with a running container.
These resources will need to be cleaned up when containerd looses the connection to a shim.
This is also used when containerd boots and reconnects to shims.
If a bundle is still on disk but containerd cannot connect to a shim, the delete command is invoked.

The delete command MUST accept the following flags:

* `-namespace` the namespace for the container
* `-id` the id of the container
* `-address` the address of the containerd's main socket
* `-publish-binary` the binary path to publish events back to containerd

The delete command will be executed in the container's bundle as its `cwd`.

### Host Level Shim Configuration

containerd does not provide any host level configuration for shims via the API.
If a shim needs configuration from the user with host level information across all instances, a shim specific configuration file can be setup.

### Container Level Shim Configuration

On the create request, there is a generic `*protobuf.Any` that allows a user to specify container level configuration for the shim.

```proto
message CreateTaskRequest {
	string id = 1;
	...
	google.protobuf.Any options = 10;
}
```

A shim author can create their own protobuf message for configuration and clients can import and provide this information is needed.

### I/O

I/O for a container is provided by the client to the shim via fifo on Linux, named pipes on Windows, or log files on disk.
The paths to these files are provided on the `Create` rpc for the initial creation and on the `Exec` rpc for additional processes.

```proto
message CreateTaskRequest {
	string id = 1;
	bool terminal = 4;
	string stdin = 5;
	string stdout = 6;
	string stderr = 7;
}
```

```proto
message ExecProcessRequest {
	string id = 1;
	string exec_id = 2;
	bool terminal = 3;
	string stdin = 4;
	string stdout = 5;
	string stderr = 6;
}
```

Containers that are to be launched with an interactive terminal will have the `terminal` field set to `true`, data is still copied over the files(fifos,pipes) in the same way as non interactive containers.

### Root Filesystems

The root filesystems for the containers is provided by on the `Create` rpc.
Shims are responsible for managing the lifecycle of the filesystem mount during the lifecycle of a container.

```proto
message CreateTaskRequest {
	string id = 1;
	string bundle = 2;
	repeated containerd.types.Mount rootfs = 3;
	...
}
```

The mount protobuf message is:

```proto
message Mount {
	// Type defines the nature of the mount.
	string type = 1;
	// Source specifies the name of the mount. Depending on mount type, this
	// may be a volume name or a host path, or even ignored.
	string source = 2;
	// Target path in container
	string target = 3;
	// Options specifies zero or more fstab style mount options.
	repeated string options = 4;
}
```

Shims are responsible for mounting the filesystem into the `rootfs/` directory of the bundle.
Shims are also responsible for unmounting of the filesystem.
During a `delete` binary call, the shim MUST ensure that filesystem is also unmounted.
Filesystems are provided by the containerd snapshotters.

### Events

The shim MUST publish a `runtime.TaskExitEventTopic` when the container exits.
If the shim collects Out of Memory events, it SHOULD also publish a `runtime.TaskOOMEventTopic`.

### Other

#### Unsupported rpcs

If a shim does not or cannot implement an rpc call, it MUST return a `github.com/containerd/containerd/errdefs.ErrNotImplemented` error.

#### Debugging and Shim Logs

A fifo on unix or named pipe on Windows will be provided to the shim.
It can be located inside the `cwd` of the shim named "log".
The shims can use the existing `github.com/containerd/containerd/log` package to log debug messages.
Messages will automatically be output in the containerd's daemon logs with the correct fields and runtime set.

#### ttrpc

[ttrpc](https://github.com/containerd/ttrpc) is the only currently supported protocol for shims.
It works with standard protobufs and GRPC services as well as generating clients.
The only difference between grpc and ttrpc is the wire protocol.
ttrpc removes the http stack in order to save memory and binary size to keep shims small.
It is recommended to use ttrpc in your shim but grpc support is also in development.
