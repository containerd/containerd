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

```.go
type Handler interface {
	Mount(context.Context, Mount, string, []ActiveMount) (ActiveMount, error)
	Unmount(context.Context, string) error
}
```

### Mount formatting

In order to chain mounts together, the results from a previous mount may be
needed for subsequent mounts. Some of these mount parameters may not be
known until mount time, making it impossible to represent with static
mount values. Formatted mounts allow providing templated values for mount
parameters to be filled in at mount activation time using the previous
mounts' results.

Formatted mounts have a type that starts with `format/` and following by the
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

### Relationship with runtimes

The runtime should use the mount manager to initiate activation of the mounts
before setting up the rootfs for a container. The runtime name should be passed
along to the activation call so that the mount manager may be configured for
runtime specific behavior. For example, a runtime is able to understand
formatting or specific mount types, then the mount manager can avoid performing
those mounts and let the runtime handle it.