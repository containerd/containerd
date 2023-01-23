# Snapshotters

Snapshotters manage the snapshots of the container filesystems.

The available snapshotters can be inspected by running `ctr plugins ls` or `nerdctl info`.

## Core snapshotter plugins

Generic:
- `overlayfs` (default): OverlayFS. This driver is akin to Docker/Moby's "overlay2" storage driver, but containerd's implementation is not called "overlay2".
- `native`: Native file copying driver. Akin to Docker/Moby's "vfs" driver.

Filesystem-specific:
- `btrfs`: btrfs. Needs the plugin root (`/var/lib/containerd/io.containerd.snapshotter.v1.btrfs`) to be mounted as btrfs.
- `zfs`: ZFS. Needs the plugin root (`/var/lib/containerd/io.containerd.snapshotter.v1.zfs`) to be mounted as ZFS. See also https://github.com/containerd/zfs .
- `devmapper`: ext4/xfs device mapper. See [`devmapper.md`](./devmapper.md).

[Deprecated](https://github.com/containerd/containerd/blob/main/RELEASES.md#deprecated-features):
- `aufs`: AUFS. Deprecated since containerd 1.5. Planned to be removed in containerd 2.0. See also https://github.com/containerd/aufs .

## Non-core snapshotter plugins

- `fuse-overlayfs`: [FUSE-OverlayFS Snapshotter](https://github.com/containerd/fuse-overlayfs-snapshotter)
- `nydus`: [Nydus Snapshotter](https://github.com/containerd/nydus-snapshotter)
- `overlaybd`: [OverlayBD Snapshotter](https://github.com/containerd/accelerated-container-image)
- `stargz`: [Stargz Snapshotter](https://github.com/containerd/stargz-snapshotter)

## Mount target

Mounts can optionally specify a target to describe submounts in the container's
rootfs. For example, if the snapshotter wishes to bind mount to a subdirectory
ontop of an overlayfs mount, they can return the following mounts:

```json
[
    {
        "type": "overlay",
        "source": "overlay",
        "options": [
            "workdir=...",
            "upperdir=...",
            "lowerdir=..."
        ]
    },
    {
        "type": "bind",
        "source": "/path/on/host",
        "target": "/path/inside/container",
        "options": [
            "ro",
            "rbind"
        ]
    }
]
```

However, the mountpoint `/path/inside/container` needs to exist for the bind
mount, so one of the previous mounts must be responsible for providing that
directory in the rootfs. In this case, one of the lower dirs of the overlay has
that directory to enable the bind mount.
