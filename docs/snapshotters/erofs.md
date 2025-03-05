# EROFS Snapshotter

The [EROFS](https://erofs.docs.kernel.org) snapshotter is an experimental
feature, which is able to leverage EROFS-formatted blobs for each committed
snapshot and prepares an EROFS + OverlayFS mount for each active snapshot.

In order to leverage EROFS-formatted blobs, the EROFS differ is needed to be
used together to apply image layers.  Otherwise, the EROFS snapshotter will
just behave as the existing OverlayFS snapshotter: the default applier will
unpack the image layer into the active EROFS snapshot, and commit it.

Although it sounds somewhat similar to an enhanced OverlayFS snapshotter but
I believe there are clear differences if looking into `s.mount()` and it highly
tightens to the EROFS internals.  Currently, it's not quite clear to form an
enhanced OverlayFS snapshotter directly, and (I think) it's not urgent since
in the very beginning, it'd be better to be left as an independent snapshotter
so that existing overlayfs users won't be impacted by the new behaviors and
users could have a chance to try and develop the related ecosystems (such as
ComposeFS, confidential containers, gVisor, Kata, gVisor, and more) together.

## Use Cases

The EROFS snapshotter can benefit to several use cases:

For runC containers, instead of unpacking individual files into a directory
on the backing filesystem, it applies OCI layers into EROFS blobs, therefore:

 - Improved image unpacking performance (~14% for WordPress image with the
   latest erofs-utils 1.8.2) due to reduced metadata overhead;

 - Full data protection for each snapshot using the FS_IMMUTABLE_FL file
   attribute and fsverity.  EROFS uses FS_IMMUTABLE_FL and fsverity to protect
   each EROFS layer blob, ensuring the mounted tree remains immutable.  However,
   since FS_IMMUTABLE_FL and fsverity protect individual files rather than a
   sub-filesystem tree, other snapshotter implementations like the overlayfs
   snapshotter are not quite applicable due to less efficiency at least;

 - Parallel unpacking can be supported in a more reliable way (fsync) compared
   to the overlayfs snapshotter (syncfs);

 - Native EROFS layers can be pulled from registries without conversion.

For VM containers, the EROFS snapshotter can efficiently pass through and share
image layers, offering several advantages (e.g. better performance and smaller
memory footprints) over [virtiofs](https://virtio-fs.gitlab.io) or
[9p](https://www.kernel.org/doc/Documentation/filesystems/9p.txt).  Besides,
the popular application kernel [gVisor](https://gvisor.dev/) also supports
[EROFS](https://github.com/google/gvisor/pull/9486) for efficient image
pass-through.

## Usage

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

Before using EROFS snapshotter, also make sure the _EROFS kernel module_ is
loaded (Linux 5.4 or later is required): it can be loaded with `modprobe erofs`.

### Configuration

The following configuration can be used in your containerd `config.toml`. Don't
forget to restart containerd after changing the configuration.

```
  [plugins."io.containerd.snapshotter.v1.erofs"]
      # Enable fsverity support for EROFS layers, default is false
      enable_fsverity = true

      # Optional: Additional mount options for overlayfs
      ovl_mount_options = []

  [plugins."io.containerd.service.v1.diff-service"]
    default = ["erofs","walking"]
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

## Data Integrity with fsverity

The EROFS snapshotter supports fsverity for data integrity verification of EROFS layers.
When enabled via `enable_fsverity = true`, the snapshotter will:
- Enable fsverity on EROFS layers during commit
- Verify fsverity status before mounting layers
- Skip fsverity if the filesystem or kernel does not support it

## TODO

The EROFS Fsmerge feature is NOT supported in the current implementation
because it was somewhat unclean (relying on `containerd.io/snapshot.ref`).
It needs to be reconsidered later.
