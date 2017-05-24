package main

// register containerd builtins here
import (
	_ "github.com/containerd/containerd/services/containers"
	_ "github.com/containerd/containerd/services/content"
	_ "github.com/containerd/containerd/services/diff"
	_ "github.com/containerd/containerd/services/execution"
	_ "github.com/containerd/containerd/services/healthcheck"
	_ "github.com/containerd/containerd/services/images"
	_ "github.com/containerd/containerd/services/metrics"
	_ "github.com/containerd/containerd/services/snapshot"
	_ "github.com/containerd/containerd/services/version"
)
