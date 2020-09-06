# [containerd](https://github.com/containerd/containerd) ZFS snapshotter plugin

[![Build Status](https://travis-ci.org/containerd/zfs.svg)](https://travis-ci.org/containerd/zfs)
[![codecov](https://codecov.io/gh/containerd/zfs/branch/master/graph/badge.svg)](https://codecov.io/gh/containerd/zfs)

ZFS snapshotter plugin for containerd.

This plugin is tested on Linux with Ubuntu.  It should be compatible with FreeBSD.

## Usage

The plugin is built-in by default since containerd 1.1.
No need to recompile containerd or execute a proxy snapshotter process.

1. Set up a ZFS filesystem.
The ZFS filesystem name is arbitrary but the mount point needs to be `/var/lib/containerd/io.containerd.snapshotter.v1.zfs`, when the containerd root is set to `/var/lib/containerd`.
```console
$ zfs create -o mountpoint=/var/lib/containerd/io.containerd.snapshotter.v1.zfs your-zpool/containerd
```

2. Start containerd.

3. e.g. `ctr pull --snapshotter=zfs ...`

## Project details

The zfs plugin is a containerd sub-project, licensed under the [Apache 2.0 license](./LICENSE).
As a containerd sub-project, you will find the:
 * [Project governance](https://github.com/containerd/project/blob/master/GOVERNANCE.md),
 * [Maintainers](https://github.com/containerd/project/blob/master/MAINTAINERS),
 * and [Contributing guidelines](https://github.com/containerd/project/blob/master/CONTRIBUTING.md)

information in our [`containerd/project`](https://github.com/containerd/project) repository.
