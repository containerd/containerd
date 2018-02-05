package defaultplugins

// nolint: golint
import (
	_ "github.com/containerd/containerd/linux"
	_ "github.com/containerd/containerd/metrics/cgroups"
	_ "github.com/containerd/containerd/snapshots/overlay"
)
