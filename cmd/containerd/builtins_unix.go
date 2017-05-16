// +build darwin freebsd

package main

import (
	_ "github.com/containerd/containerd/snapshot/naive"
	_ "github.com/containerd/containerd/snapshot/zfs"
)
