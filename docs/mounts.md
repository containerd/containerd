# containerd Mounts and Mount Management

## Mount Type

`Mount` is an important struct in containerd used to represent a filesystem
without needing any active state. This allows deferring the mounting of
filesystems to when they are needed. This also allows temporary mounts, normally
done by containerd to inspect a container image's filesystem or make changes
before handing off a filesystem to the lower level runtime. This also allows the
lower level runtime to make optimizations such as using virtio-blk over
virtio-fs for some mounts or performing the mounts inside another mount
namespace.

The `Mount` type is used by the `Snapshotter` interface to return the filesystem
for a snapshot. This allows the snapshotter to focus on the storage lifecycle of
the snapshot without complicated logic to handle the runtime lifecycle of the
mounted filesystem. This is part of containerd's decoupled architecture where
snapshotters and runtimes don't need to share state, only the set of mounts
needs to be communicated.

## Mount Management

In containerd 2.2, the mount manager was introduced to extend the functionality
of mounts, allowing for more powerful snapshotters and complex use cases that
are hard to represent with native filesystem mounts. It also adds an extra
layer of resource tracking to mounts, to add more protection against leaking
mounts in the host mount namespace.

### Extending the mount types

Typically snapshotters are limited to mount types which are mountable by
the host kernel, or in some advanced use cases, a vm guest kernel. The mount
manager is able to extend the mount types by using a plugable interface to
handle custom mount types.

The interface for a custom mount handler is very simple.

```go
type Handler interface {
	Mount(context.Context, Mount, string, []ActiveMount) (ActiveMount, error)
	Unmount(context.Context, string) error
}
```

#### Built-in Mount Handlers

##### Loopback Handler

The loopback handler (`loop`) allows mounting files as loopback devices. This is useful
for mounting disk images or filesystem images without requiring a pre-configured loopback device.
It is preferable to use the `loop` option on mounts when supported by other mount types to allow
other mount types to optimize when a loopback is needed, when this handler is used with
another mount type it may force a loopback to be used even when not necessary.

```go
// Example mount using loopback
mount.Mount{
    Type:    "loop",
    Source:  "/path/to/disk.img",
    Options: []string{},
}
```

The handler automatically:
- Sets up the loopback device using the first available loop device
- Makes device available at mount point
- Handles cleanup on unmount

### Mount Transformers

Mount transformers are interfaces that can modify mounts based on previous mount state.
Transformers are useful for preparing mounts before they are activated, such as creating
directories or formatting filesystems.

```go
type Transformer interface {
	Transform(context.Context, Mount, []ActiveMount) (Mount, error)
}
```

Transformers are specified in the mount type using a prefix pattern: `<transformer>/<mount-type>`.
Multiple transformers can be chained: `<transformer1>/<transformer2>/<mount-type>`.

#### Built-in Transformers

##### Format Transformer (`format/`)

In order to chain mounts together, the results from a previous mount may be
needed for subsequent mounts. Some of these mount parameters may not be
known until mount time, making it impossible to represent with static
mount values. Formatted mounts allow providing templated values for mount
parameters to be filled in at mount activation time using the previous
mounts' results.

Formatted mounts have a type that starts with `format/` followed by the
intended mount type after filling in format values.
Uses templating based on [go templates](https://pkg.go.dev/text/template) to
fill in values.

Values are referenced by the index of the previous active mounts. The following
is a list of supported values that can be provided in formatted mounts.

| Value | Args | Example | Description |
|-----|------|---------|-------------|
| `source` | <index> | `{{ source 0 }}` | Source from active mount at <index> |
| `target` | <index> | `{{ target 0 }}` | Target from the active mount at <index> |
| `mount` | <index> | `{{ mount 0 }}` | Mount point from the active mount at <index> |
| `overlay` | <start> <end> | `{{ overlay 0 2 }}` | Fill in overlayfs lowerdir arguments for active mount points at <start> through <end> |

Formatted mounts are handled differently than other custom mounts. If the
resulting mount after formatting is a supported system mount, it does not
need to be mounted by the mount handlers like custom mounts do.

**Example:**
```go
// First mount provides the lower layer
mount.Mount{
    Type:   "bind",
    Source: "/var/lib/containerd/snapshots/1",
    Options: []string{"ro"},
},
// Second mount uses formatting to reference the first mount
mount.Mount{
    Type:   "format/overlay",
    Source: "overlay",
    Options: []string{
        "lowerdir={{ mount 0 }}",
        "upperdir=/upper",
        "workdir=/work",
    },
}
```

##### Mkfs Transformer (`mkfs/`)

The mkfs transformer creates and formats filesystem images. It supports creating
ext2, ext3, ext4, and xfs filesystems in files that can then be mounted as loopback devices.

Mount options with the `X-containerd.mkfs.` prefix are consumed by the transformer:

| Option | Description | Example |
|--------|-------------|---------|
| `X-containerd.mkfs.size` | Size of the filesystem image (supports units like MiB, GiB) | `X-containerd.mkfs.size=100MiB` |
| `X-containerd.mkfs.fs` | Filesystem type (ext2, ext3, ext4, xfs) | `X-containerd.mkfs.fs=ext4` |
| `X-containerd.mkfs.uuid` | UUID for the filesystem | `X-containerd.mkfs.uuid=550e8400-e29b-41d4-a716-446655440000` |

**Example:**
```go
mount.Mount{
    Type:   "mkfs/loop",
    Source: "/path/to/disk.img",
    Options: []string{
        "X-containerd.mkfs.size=1GiB",
        "X-containerd.mkfs.fs=ext4",
    },
}
```

This will:
1. Create a 1GiB file at `/path/to/disk.img`
2. Format it as ext4
3. Set up a loopback device for the file
4. Return the loop device for subsequent mounting

##### Mkdir Transformer (`mkdir/`)

The mkdir transformer creates directories before mounting. This is useful for
ensuring overlay upperdir and workdir directories exist, or for creating mount points.

Mount options with the `X-containerd.mkdir.` prefix are consumed by the transformer:

| Option Format | Description |
|--------------|-------------|
| `X-containerd.mkdir.path=<dir>` | Create directory with default permissions (0700) |
| `X-containerd.mkdir.path=<dir>:<mode>` | Create directory with specified octal mode |
| `X-containerd.mkdir.path=<dir>:<mode>:<uid>:<gid>` | Create directory with mode and ownership |

**Example:**
```go
mount.Mount{
    Type:   "format/mkdir/overlay",
    Source: "overlay",
    Options: []string{
        "X-containerd.mkdir.path={{ mount 0 }}/upper:0755",
        "X-containerd.mkdir.path={{ mount 0 }}/work:0755",
        "lowerdir={{ mount 1 }}",
        "upperdir={{ mount 0 }}/upper",
        "workdir={{ mount 0 }}/work",
    },
}
```

#### Chaining Transformers

Transformers can be chained together to perform multiple operations in sequence:

```go
mount.Mount{
    Type:   "mkfs/loop",
    Source: "/data/fs.img",
    Options: []string{
        "X-containerd.mkfs.size=500MiB",
        "X-containerd.mkfs.fs=xfs",
    },
},
mount.Mount{
    Type:   "xfs",
    Source: "{{ source 0 }}",  // Loop device from previous mount
    Options: []string{},
},
mount.Mount{
    Type:   "format/mkdir/overlay",
    Source: "overlay",
    Options: []string{
        "X-containerd.mkdir.path={{ mount 1 }}/upper:0755",
        "X-containerd.mkdir.path={{ mount 1 }}/work:0755",
        "lowerdir=/lower",
        "upperdir={{ mount 1 }}/upper",
        "workdir={{ mount 1 }}/work",
    },
}
```

This example:
1. Creates and formats a 500MiB XFS image
2. Sets up a loop device and mounts the XFS filesystem
3. Creates directories on the XFS filesystem and sets up an overlay

### Garbage Collection and Backreferences

The mount manager integrates with containerd's garbage collection system to ensure
mounts are properly tracked and cleaned up. Mounts can reference other resources
using special labels:

| Label | Description |
|-------|-------------|
| `containerd.io/gc.bref.container.*` | Back reference to a container |
| `containerd.io/gc.bref.content.*` | Back reference to content in the content store |
| `containerd.io/gc.bref.image.*` | Back reference to an image |
| `containerd.io/gc.bref.snapshot.*` | Back reference to a snapshot |

The `.*` suffix allows for named backreferences separated by `.` or `/`.

**Example:**
```go
info, err := mountManager.Activate(ctx, "my-mount", mounts,
    mount.WithLabels(map[string]string{
        "containerd.io/gc.bref.container": "container-id-123",
        "containerd.io/gc.bref.snapshot.overlayfs": "active-snapshot-key",
    }),
)
```

These labels ensure that the mount won't be garbage collected while the
referenced resources still exist, and the mount will be automatically cleaned
up when the references are removed.

### Relationship with runtimes

The runtime should use the mount manager to initiate activation of the mounts
before setting up the rootfs for a container. The runtime name should be passed
along to the activation call so that the mount manager may be configured for
runtime specific behavior.

The `ActivateOptions` allow runtimes to indicate which mount types they can handle:

```go
// Runtime can handle formatting, so don't let mount manager do it
info, err := mountManager.Activate(ctx, name, mounts,
    mount.WithAllowMountType("format/*"),
)

// Runtime can handle loop devices
info, err := mountManager.Activate(ctx, name, mounts,
    mount.WithAllowMountType("loop"),
)
```

#### Support with containerd shims

By default, the containerd runtime will call the mount manager to activate mounts,
which will perform any transformations and custom mounts. However, a runtime shim may
choose to handle some mount types or transformations itself in order to optimize
performance based on the runtime environment. For example, a VM based runtime may
choose to handle loopback mounts itself by passing the disk image file directly to
the VM instead of setting up a loop device on the host. The runtime shim may export
the annotation `containerd.io/runtime-allow-mounts` in its runtime info to indicate
which mount types the shim can handle. The values are comma separated and passed to
via the `mount.WithAllowMountType` option when activating mounts.

### Mount Manager Interface

The complete mount manager interface:

```go
type Manager interface {
    Activate(context.Context, string, []Mount, ...ActivateOpt) (ActivationInfo, error)
    Deactivate(context.Context, string) error
    Info(context.Context, string) (ActivationInfo, error)
    Update(context.Context, ActivationInfo, ...string) (ActivationInfo, error)
    List(context.Context, ...string) ([]ActivationInfo, error)
}
```

**Methods:**
- `Activate`: Activate a set of mounts with a unique name
- `Deactivate`: Unmount and cleanup an activation
- `Info`: Get information about an active mount
- `Update`: Update an active mount (not yet implemented)
- `List`: List all active mounts, optionally filtered

**ActivationInfo** contains:
- `Name`: Unique identifier for the activation
- `Active`: Mounts that were handled by the mount manager
- `System`: Remaining mounts that must be performed by the system/runtime
- `Labels`: Labels associated with the activation

### Storage and Persistence

The mount manager stores activation state in a BoltDB database and maintains
mount targets in a dedicated directory. This provides:

- Crash recovery: Mounts can be tracked and cleaned up after daemon restart
- Garbage collection: Integration with containerd's GC system
- Lease support: Mounts can be associated with leases for lifecycle management

### Example Usage

```go
// Initialize mount manager
mm, err := manager.NewManager(
    db,
    targetDir,
    manager.WithMountHandler("loop", mount.LoopbackHandler()),
)

// Create mounts for a writable overlay with custom block device
mounts := []mount.Mount{
    {
        Type:   "mkfs/loop",
        Source: "/data/writable.img",
        Options: []string{
            "X-containerd.mkfs.size=1GiB",
            "X-containerd.mkfs.fs=ext4",
        },
    },
    {
        Type:   "ext4",
        Source: "{{ source 0 }}",
    },
    {
        Type:   "mkdir/format/overlay",
        Source: "overlay",
        Options: []string{
            "X-containerd.mkdir.path={{ mount 1 }}/upper:0755",
            "X-containerd.mkdir.path={{ mount 1 }}/work:0755",
            "lowerdir=/snapshots/base",
            "upperdir={{ mount 1 }}/upper",
            "workdir={{ mount 1 }}/work",
        },
    },
}

// Activate with lease and backreference
info, err := mm.Activate(ctx, "container-123-rootfs", mounts,
    mount.WithLabels(map[string]string{
        "containerd.io/gc.bref.container": "container-123",
    }),
)

// info.Active contains mounts handled by mount manager
// info.System contains remaining mounts to perform in container namespace

// Later, cleanup
err = mm.Deactivate(ctx, "container-123-rootfs")
```