package main

import (
	"os"
	"path/filepath"
)

func defaultConfig() *config {
	return &config{
		Root: filepath.Join(os.Getenv("programfiles"), "containerd", "root"),
		GRPC: grpcConfig{
			Address: `\\.\pipe\containerd-containerd`,
		},
		Debug: debug{
			Level:   "info",
			Address: `\\.\pipe\containerd-debug`,
		},
		Snapshotter: "io.containerd.snapshotter.v1.windows",
		Differ:      "io.containerd.differ.v1.base-diff",
	}
}
