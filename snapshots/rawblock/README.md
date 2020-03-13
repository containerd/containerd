## Rawblock snapshotter

Rawblock is a `containerd` snapshotter plugin that stores snapshots as block files on the local file system. It makes
use of the local file system's [reflink cow capability](http://man7.org/linux/man-pages/man1/cp.1.html) when it is
avaialbe to share file data among snapshots and save disk space.

## Setup

Rawblock snapshotter formats the snapshots as `ext4`(default) or `xfs` file systems. Make sure you have corresponding
mkfs binary (`mkfs.ext4` or `mkfs.xfs`) installed.

It is also possible to configure the rawblock snapshotter options in containerd's configuration file,
like `/etc/containerd/config.toml`. E.g,

```
[plugins]
  ...
  [plugins.rawblock]
    root_path = "/mnt/images"
    size_MB = 2048
    fs_type = "xfs"
    options = ["nouuid","noatime"]
  ...
```

The following configuration flags are supported:
* `root_path` - a directory where the snapshots are saved (if unset,
  default location for `containerd` plugins will be used).
* `size_mb` - size of the base snapshot file (if unset, default to `1024` MB).
* `fs_type` - the snapshot file will be formated with the specified file system type, only "ext4"
  and "xfs" are currently supported (if unset, default to `ext4`).
* `options` - extra slice of string options to mount the snapshot file as a local file system.

## Run
Give it a try with the following commands:

```bash
ctr images pull --snapshotter rawblock docker.io/library/hello-world:latest
ctr run --snapshotter rawblock docker.io/library/hello-world:latest test
```

## Requirements
* To format snapshot images, corresponding mkfs binary is required per `fs_type` option.
* To mount snapshot images, corresponding file system kernel module is required per `fs_type` option.
* To make loopback device use direct IO, kernel version (>= 4.4) is required for the `LOOP_SET_DIRECT_IO` ioctl, otherwise
  it falls back to buffer IO and loopback mounts suffer from double cache problem.
* To support loopback device auto clearing, kenrel version (>= v2.6.25) is required for the `LO_FLAGS_AUTOCLEAR` ioctl.
