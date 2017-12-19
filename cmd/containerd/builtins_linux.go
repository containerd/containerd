package main

import (
	_ "github.com/containerd/containerd/linux"
	_ "github.com/containerd/containerd/metrics/cgroups"
	_ "github.com/containerd/containerd/snapshots/aufs"
	_ "github.com/containerd/containerd/snapshots/overlay"
	_ "github.com/containerd/containerd/snapshots/zfs"
)
