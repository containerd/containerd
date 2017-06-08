// +build darwin freebsd

package main

func defaultConfig() *config {
	return &config{
		Root: "/var/lib/containerd",
		GRPC: grpcConfig{
			Address: "/run/containerd/containerd.sock",
		},
		Debug: debug{
			Level:   "info",
			Address: "/run/containerd/debug.sock",
		},
		Snapshotter: "io.containerd.snapshotter.v1.naive",
		Differ:      "io.containerd.differ.v1.base-diff",
	}
}
