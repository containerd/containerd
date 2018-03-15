# [containerd](https://github.com/containerd/containerd) ZFS snapshotter plugin

[![Build Status](https://travis-ci.org/containerd/zfs.svg)](https://travis-ci.org/containerd/zfs)
[![codecov](https://codecov.io/gh/containerd/zfs/branch/master/graph/badge.svg)](https://codecov.io/gh/containerd/zfs)

ZFS snapshotter plugin for containerd.

This plugin is tested on Linux with Ubuntu.  It should be compatible with FreeBSD.


## Compile

To compile containerd with ZFS support, add the import into the `$GOPATH/src/github.com/containerd/containerd/cmd/containerd/builtins_zfs.go` file.

```go
// +build linux freebsd

package main

import (
        _ "github.com/containerd/zfs"
)
```

Please refer to [`.travis.yml`](.travis.yml) for the latest containerd version known to work with.


## Usage

1. Set up a ZFS filesystem.
The ZFS filesystem name is arbitrary but the mount point needs to be `/var/lib/containerd/io.containerd.snapshotter.v1.zfs`, when the containerd root is set to `/var/lib/containerd`.
```console
$ zfs create -o mountpoint=/var/lib/containerd/io.containerd.snapshotter.v1.zfs your-zpool/containerd
```

2. Start containerd.

3. e.g. `ctr pull --snapshotter=zfs ...`
