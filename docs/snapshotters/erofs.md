# EROFS Snapshotter

The [EROFS](https://erofs.docs.kernel.org) snapshotter is a native containerd
snapshotter to enable the EROFS filesystem, specifically to keep EROFS‑formatted
blobs for each committed snapshot and to prepare an OverlayFS mount for each
active snapshot.

Although the EROFS snapshotter sounds somewhat similar to an enhanced OverlayFS
snapshotter, several kernel features are highly tied to the EROFS internals, so
it would be better to leave it as an independent snapshotter. This way, existing
OverlayFS users will not be impacted by the new EROFS‑specific behaviors, and
interested users will also have a chance to use the EROFS filesystem and even
develop the related ecosystems (such as ComposeFS, confidential containers,
gVisor, Kata, nerdbox and more) together.

## Use Cases

The EROFS snapshotter can benefit to several use cases:

For runC containers, instead of unpacking individual files into a directory
on the backing filesystem, it applies OCI layers into EROFS blobs, therefore:

 - Improved image unpacking performance (~14% for WordPress image with the
   latest erofs-utils 1.8.2) due to reduced metadata overhead;

 - Parallel unpacking is now supported natively, similar to the OverlayFS
   snapshotter.  This capability is difficult to implement in
   disk‑snapshot‑style snapshotters such as blockfile, devmapper and ZFS
   snapshotters.  It also uses an efficient method to persist layer data (via
   fsync) compared to the OverlayFS snapshotter, which can only use syncfs;

 - Support given‑size block devices as the upper layer for OverlayFS to
   limit the disk quota for writable layers (usually ephemeral storage);

 - A dedicated EROFS default mount handler enables EROFS file‑backed mounts
   to avoid loop devices on runC.  Note that specific runtime shims can
   handle EROFS mounts without this built‑in handler; for more details, see
   [containerd Mounts and Mount Management](../mounts.md);

 - Full data protection for each snapshot using the FS_IMMUTABLE_FL file
   attribute and fsverity.  EROFS uses FS_IMMUTABLE_FL and fsverity to protect
   each EROFS layer blob, ensuring the mounted tree remains immutable.  However,
   since FS_IMMUTABLE_FL and fsverity protect individual files rather than a
   sub-filesystem tree, other snapshotter implementations like the overlayfs
   snapshotter are not quite applicable due to less efficiency at least;

 - Native EROFS layers can be pulled from registries without conversion.

For VM containers, the EROFS snapshotter can efficiently pass through and share
image layers, offering several advantages (e.g. better performance and smaller
memory footprints) over [virtiofs](https://virtio-fs.gitlab.io) or
[9p](https://www.kernel.org/doc/Documentation/filesystems/9p.txt).  Besides,
the popular application kernel [gVisor](https://gvisor.dev/) also supports
[EROFS](https://github.com/google/gvisor/pull/9486) for efficient image
pass-through.

## Usage

### Ensure that EROFS filesystem is available

On newer Ubuntu/Debian systems, erofs-utils can be installed directly using the
apt command, and on Fedora it can be installed directly using the dnf command.

```bash
# Debian/Ubuntu
$ apt install erofs-utils
# Fedora
$ dnf install erofs-utils
```

Make sure that erofs-utils version is 1.7 or higher.

When using the EROFS snapshotter, before starting containerd, also make sure
the _EROFS kernel module_ is loaded (Linux 5.4 or later is required): it can be
loaded with `modprobe erofs`.

### Checking if the EROFS snapshotter and differ are available

To check if the EROFS snapshotter is available, run the following command:

```bash
$ ctr plugins ls | grep erofs
```

The following message will be shown like below:
```
io.containerd.snapshotter.v1           erofs                    linux/amd64    ok
io.containerd.differ.v1                erofs                    linux/amd64    ok
```

### Configuration

The following configuration can be used in your containerd `config.toml`. Don't
forget to restart containerd after changing the configuration.

``` toml
  [plugins."io.containerd.snapshotter.v1.erofs"]
      # Enable fsverity support for EROFS layers, default is false
      enable_fsverity = true

      # Optional: Additional mount options for overlayfs
      ovl_mount_options = []

  [plugins."io.containerd.service.v1.diff-service"]
    default = ["erofs","walking"]
```

Note that if erofs-utils is version 1.8 or higher, you can add
`-T0 --mkfs-time` to the differ's `mkfs_options` to enable reproducible
builds, as shown below:

``` toml
  [plugins."io.containerd.differ.v1.erofs"]
    mkfs_options = ["-T0", "--mkfs-time"]
```

If erofs-utils is 1.8.2 or higher, it's preferred to append `--sort=none` to
the differ's `mkfs_options` to avoid unnecessary tar data reordering for
improved performance, as shown below:

``` toml
  [plugins."io.containerd.differ.v1.erofs"]
    mkfs_options = ["-T0", "--mkfs-time", "--sort=none"]
```

### Running a container

To run a container using the EROFS snapshotter, it needs to be explicitly
specified:

```bash
$ # ensure that the image we are using exists; it is a regular OCI image
$ ctr image pull docker.io/library/busybox:latest
$ # run the container with the provides snapshotter
$ ctr run -rm -t --snapshotter erofs docker.io/library/busybox:latest hello sh
```

## Quota Support

EROFS supports block mode to generate fixed‑size virtual blocks as the upper
layers for overlayfs with a given filesystem formatted in order enable the disk
quota.  The `default_size` option can be used in the containerd configuration:

```toml
  [plugins."io.containerd.snapshotter.v1.erofs"]
    default_size = "20GiB"
```

## Data Integrity

The EROFS snapshotter provides two methods to consolidate data integrity:

### Data Integrity with Immutable File Attribute

By setting `set_immutable = true`, the EROFS snapshotter marks each layer blob
with `IMMUTABLE_FL`. This ensures that dirty data is flushed immediately and the
EROFS layer blob cannot be deleted, renamed, or modified.

The immutable file attribute is mainly used to ensure data persistence and
prevent artificial data loss, but it cannot detect data corruption caused by
hardware failures. Since it can flush in-memory dirty data, it may significantly
increase the unpacking time it takes to launch a container: for example, the
unpacking time for tensorflow:2.19.0 increases by 108.86% (from 10.090s to
21.074s) on EXT4. However, it has no impact on runtime performance.

### Data Integrity with fs-verity

By setting `enable_fsverity = true`, the EROFS snapshotter will:

 - Enable fs-verity on EROFS layers during commit;

 - Verify the fs-verity status before mounting layers;

 - Skip fs-verity if the filesystem or kernel does not support it.

The fs-verity method guarantees that EROFS blob layers never change, but it
introduces additional runtime overhead since all container image reads from
the container will be slower because it needs to verify the Merkle hash tree
first.

## How It Works

For each layer, the EROFS snapshotter prepares a directory containing the
following items:

```
  .erofslayer
  fs
  work
```

`.erofslayer` file is used to indicate that the layer is prepared by the EROFS
snapshotter.

If the EROFS differ is also enabled, the differ will check for the existence
of `.erofslayer` and convert the image content blob (e.g., an OCI layer) into
an EROFS layer blob.

In this case, the snapshot layer directory will look like this:
```
  .erofslayer
  fs
  layer.erofs
  work
```

Then the EROFS snapshotter will check for the existence of `layer.erofs`: it
will mount the EROFS layer blob to `fs/` and return a valid overlayfs mount
with all parent layers.

If other differs (not the EROFS differ) are used, the EROFS snapshotter will
convert the flat directory into an EROFS layer blob on Commit instead.

In other words, the EROFS differ can only be used with the EROFS snapshotter;
otherwise, it will skip to the next differ.  The EROFS snapshotter can work
with or without the EROFS differ.

## Tar Index Mode

The EROFS differ also supports a "tar index" mode that offers a unique approach to handling OCI image layers:

Instead of extracting the entire tar archive to create an EROFS filesystem, the tar index mode:
1. Generates a tar index for the tar content
2. Appends the original tar content to the index
3. Creates a combined file: `[Tar index][Original tar content]`

The tar index can be stored in a registry alongside image layers, allowing nodes to fetch it directly when needed. Typically, the tar index is much smaller than a full EROFS blob, making it more efficient to store and transfer. If the tar index is not available in the registry, it can be generated on the node as a fallback. When integrating with dm-verity, the registry can also store the dm-verity Merkle tree and root hash signature together with the tar index, enabling nodes to retrieve all necessary artifacts without redundant computation.

In addition, we have a tar diffID for each layer according to the OCI image spec, so we don't need to reinvent a new way to verify the image layer content for confidential containers but just calculate the sha256 of the original tar data (because erofs could just reuse the tar data with 512-byte fs block size and build a minimal index for direct mounting of tar) out of the tar index mode in the guest and compare it with each diffID.

### Configuration

For the EROFS differ:

```toml
[plugins."io.containerd.differ.v1.erofs"]
  enable_tar_index = true
```

## TODO

 - EROFS Flatten filesystem support (EROFS fsmerge feature);

 - ID-mapped mount spport;

 - DMVerity support.
