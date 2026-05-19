# Blockfile Snapshotter

The blockfile snapshotter uses raw block files for each snapshot. Block files are
copied from a parent or base empty block file. Mounting requires a virtual machine
or support for loopback mounts.

## Use Case

Snapshotters serve the purpose of extracting an image from the OCI image store and
creating a snapshot that is useful to containers. It handles setting up the
underlying infrastructure, such as preparing a directory or other filesystem setup,
applying the layers to create a single mountable directory to serve as the container
base, and mounting into the container upon start.

The most commonly used snapshotter is the overlayfs snapshotter, which is the default
in containerd. The overlayfs snapshotter provides a directory on the host filesystem,
which then is bind-mounted into the container.

The blockfile snapshotter targets a use case where the container will run inside a
VM. Specifically, the OCI image will be the filesystem for the container, like with
a normal container, but the container itself will be run inside a VM.
Since the VM cannot bind-mount directories from the host, the blockfile snapshotter
creates a block device for the snapshot, which can be attached to the VM as a block
device to facilitate getting the contents into the guest.

This pairs naturally with VM-based container runtimes such as
[Kata Containers](https://katacontainers.io/), which can attach each snapshot's
blockfile to the guest as a virtio-blk device. See the
[Using with Kata Containers](#using-with-kata-containers) section below for a
minimal example.

## Alternatives

There are alternatives to the blockfile snapshotter for mounting directories into a
VM. One alternative is a [virtiofs](https://virtio-fs.gitlab.io) driver,
assuming your VMM supports it. Similarly, you can use
[9p](https://www.kernel.org/doc/Documentation/filesystems/9p.txt) to mount a local
directory into the VM, assuming your VMM supports it.

Additionally, the [devicemapper snapshotter](./devmapper.md) can be used to create
snapshots on filesystem images in a devicemapper thin-pool.

## Usage

### Checking if the blockfile snapshotter is available

To check if the blockfile snapshotter is available, run the following command:

```bash
$ ctr plugins ls | grep blockfile
```

### Configuration

To configure the snapshotter, you can use the following configuration options
in your containerd `config.toml`. Don't forget to restart it after changing the
configuration.

```toml
  [plugins.'io.containerd.snapshotter.v1.blockfile']
    scratch_file = "/opt/containerd/blockfile"
    root_path = "/somewhere/on/disk"
    fs_type = 'ext4'
    mount_options = []
    recreate_scratch = true
```

- `root_path`: The directory where the block files are stored. This directory must be writable by the containerd process.
- `scratch_file`: The path to the empty file that will be used as the base for the block files. This file should exist before first using the snapshotter.
- `fs_type`: The filesystem type to use for the block files. Currently supported are `ext4` and `xfs`. Prefer `xfs` formatted with `reflink=1` (the default in modern `xfsprogs`) when the host filesystem holding `root_path` and `scratch_file` also supports reflinks (XFS with `reflink=1`, or btrfs) — see [How It Works](#how-it-works) for the storage implications.
- `mount_options`: Additional mount options to use when mounting the block files.
- `recreate_scratch`: If set to `true`, the snapshotter will recreate the scratch file if it is missing. If set to `false`, the snapshotter will fail if the scratch file is missing.

### Creating the scratch file

You can create a scratch file as follows. This example uses a 500MB scratch file.

```bash
$ # make a 500M file
$ dd if=/dev/zero of=/opt/containerd/blockfile bs=1M count=500
500+0 records in
500+0 records out
524288000 bytes (524 MB, 500 MiB) copied, 1.76253 s, 297 MB/s

$ # format the file with ext4
$ sudo mkfs.ext4 /opt/containerd/blockfile
mke2fs 1.47.0 (5-Feb-2023)
Discarding device blocks: done
Creating filesystem with 512000 1k blocks and 128016 inodes
Filesystem UUID: d9947ecc-722d-4627-9cf9-fa2a3b622106
Superblock backups stored on blocks:
        8193, 24577, 40961, 57345, 73729, 204801, 221185, 401409

Allocating group tables: done
Writing inode tables: done
Creating journal (8192 blocks): done
Writing superblocks and filesystem accounting information: done
```

If you instead want to use XFS, format the scratch file with reflink support so
that subsequent per-layer copies can be cloned by the host filesystem (see
[How It Works](#how-it-works)):

```bash
$ # make a 500M file
$ dd if=/dev/zero of=/opt/containerd/blockfile bs=1M count=500

$ # format the file with xfs, enabling reflink
$ sudo mkfs.xfs -m reflink=1 /opt/containerd/blockfile
```

For the reflink fast path to actually take effect, the host filesystem that
contains `root_path` and `scratch_file` must itself support reflinks — XFS
created with `reflink=1` or btrfs. On such hosts, copying a parent blockfile to
a new snapshot becomes a copy-on-write clone, so the on-disk cost of each new
layer is bounded by the size of its delta rather than the size of the full
image.

### Running a container

To run a container using the blockfile snapshotter, you need to specify the
snapshotter:

```bash
$ # ensure that the image we are using exists; it is a regular OCI image
$ ctr image pull docker.io/library/busybox:latest
$ # run the container with the provides snapshotter
$ ctr run -rm -t --snapshotter blockfile docker.io/library/busybox:latest hello sh
```

To use it via the go client API, it is identical to using any other snapshotter:

```go
import (
    "context"
    "github.com/containerd/containerd"
    "github.com/containerd/containerd/snapshots"
)

// create a new client
client, err := containerd.New("/run/containerd/containerd.sock")
snapshotter := "blockfile"
cOpts := []containerd.NewContainerOpts{
				containerd.WithImage(image),
				containerd.WithImageConfigLabels(image),
				containerd.WithAdditionalContainerLabels(labels),
				containerd.WithSnapshotter(snapshotter)
}
container, err := client.NewContainer(ctx, containerID, cOpts...)
```

### Using with Kata Containers

[Kata Containers](https://katacontainers.io/) is a typical consumer of the
blockfile snapshotter: each snapshot is a self-contained filesystem image that
Kata can hand to the guest as a virtio-blk device, avoiding bind mounts or
9p/virtiofs entirely.

Assuming the `io.containerd.kata.v2` runtime is already installed and
discoverable by containerd, a container can be launched against the blockfile
snapshotter with `ctr`:

```bash
$ ctr run --rm -t \
    --runtime io.containerd.kata.v2 \
    --snapshotter blockfile \
    docker.io/library/busybox:latest hello sh
```

The same wiring works through the CRI path by selecting both the runtime class
backed by `io.containerd.kata.v2` and the `blockfile` snapshotter for the
runtime handler. Refer to the [Kata Containers
documentation](https://github.com/kata-containers/kata-containers/tree/main/docs)
for the guest-side configuration (image attachment mode, rootfs type, etc.),
which is independent of containerd.

## How It Works

The blockfile snapshotter functions similarly to other snapshotters.
It unpacks each individual layer from a container image, with each layer unpack
building on the content from its parent(s).

The blockfile snapshotter is unique in two ways:

1. It applies layers inside a disk image file, rather than on the host filesystem.
1. It creates a block image file for each layer, applying the previous on top of it.

Rather than a single directory with the contents, the end of the blockfile
snapshotter's process is a single file, which has the contents of the full
filesystem image. That image file can be loopback mounted, or attached to a virtual
machine.

For every layer the snapshotter creates a new blockfile, starting with a copy of the
blockfile from the previous layer. If there is no previous layer, i.e. for the first
layer, it copies the scratch file.

For example, for an image with 3 layers - called A, B, C - the process is as follows:

1. Layer A:
   1. Copy the scratch file to a new blockfile for layer A.
    1. Loopback-mount the blockfile for layer A.
    1. Apply layer A to the mount.
    1. Unmount the blockfile for layer A.
1. Layer B:
    1. Copy the blockfile for layer A to a new blockfile for layer B.
    1. Loopback-mount the blockfile for layer B.
    1. Apply layer B to the mount.
    1. Unmount the blockfile for layer B.
1. Layer C:
    1. Copy the blockfile for layer B to a new blockfile for layer C.
    1. Loopback-mount the blockfile for layer C.
    1. Apply layer C to the mount.
    1. Unmount the blockfile for layer C.

Each unpack of a layer builds upon the contents of the previous layers into a new
blockfile. This completes with the final blockfile containing the full filesystem
image.

As a result of the process, each layer leads to another blockfile in the system:

1. Layer A blockfile: contents of layer A
1. Layer B blockfile: contents of layer A + layer B
1. Layer C blockfile: contents of layer A + layer B + layer C

If available in the underlying filesystem and the host OS, the process uses
sparse file support whenever available. This means that the blockfiles only take
up the space required for the actual content.

For example, if the scratch image is 500MB, and each layer adds 25MB, then the
file sizes will be:

1. Layer A blockfile: 25MB from layer A
1. Layer B blockfile: 50MB from layer A and B
1. Layer C blockfile: 75MB from layer A, B, and C

Total space usage thus is 25+50+75=150MB. This is a fraction of the amount
required if each layer's blockfile used the full 500MB, i.e. 1500MB in total.

### Reflink-aware copies

On host filesystems that support reflinks (XFS created with `reflink=1`, or
btrfs), the per-layer copy step described above is implicitly turned into a
copy-on-write clone:

- **Linux**: the snapshotter's copy goes through Go's `io.Copy` between two
  `*os.File` handles, which delegates to the kernel's `copy_file_range(2)`
  syscall. On a reflink-capable filesystem, `copy_file_range(2)` shares extents
  rather than duplicating them, so each per-layer copy completes almost
  instantly and consumes no additional space until either side is written.
- **Darwin**: the snapshotter explicitly calls `clonefile(2)`, which provides
  the equivalent behaviour on APFS.

The end result is that the actual on-disk usage drops further than the
sparse-file numbers above: the unchanged extents of the parent blockfile are
shared with the child, and only blocks modified by the new layer cost real
space. In other words, when the underlying filesystem supports reflinks, the
blockfile snapshotter's storage efficiency is comparable to copy-on-write
snapshotters like overlayfs or btrfs.

If the underlying filesystem does not support reflinks, the snapshotter still
works correctly — the copy simply falls back to a full data copy (with sparse
holes preserved where possible).
