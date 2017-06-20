package main

import "github.com/containerd/containerd"

func defaultConfig() *config {
	return &config{
		Root: "/var/lib/containerd",
		GRPC: grpcConfig{
			Address: containerd.DefaultAddress,
		},
		Debug: debug{
			Level:   "info",
			Address: "/run/containerd/debug.sock",
		},
		Snapshotter: "io.containerd.snapshotter.v1.overlayfs",
		Differ:      "io.containerd.differ.v1.base-diff",
	}
}
