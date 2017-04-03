package main

// register containerd builtins here
import (
	_ "github.com/containerd/containerd/linux"
	_ "github.com/containerd/containerd/metrics/cgroups"
	_ "github.com/containerd/containerd/services/content"
	_ "github.com/containerd/containerd/services/execution"
	_ "github.com/containerd/containerd/services/healthcheck"
	_ "github.com/containerd/containerd/services/metrics"
	_ "github.com/containerd/containerd/services/rootfs"
	_ "github.com/containerd/containerd/snapshot/btrfs"
	_ "github.com/containerd/containerd/snapshot/overlay"
)
