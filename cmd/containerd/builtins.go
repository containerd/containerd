package main

// register containerd builtins here
import (
	_ "github.com/docker/containerd/linux"
	_ "github.com/docker/containerd/services/content"
	_ "github.com/docker/containerd/services/execution"
	_ "github.com/docker/containerd/services/healthcheck"
	_ "github.com/docker/containerd/snapshot/btrfs"
	_ "github.com/docker/containerd/snapshot/overlay"
)
