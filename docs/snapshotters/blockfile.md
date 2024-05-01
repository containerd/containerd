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
- `fs_type`: The filesystem type to use for the block files. Currently supported are `ext4` and `xfs`.
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
