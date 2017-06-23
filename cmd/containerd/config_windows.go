package main

import (
	"os"
	"path/filepath"

	"github.com/containerd/containerd/server"
)

func defaultConfig() *server.Config {
	return &server.Config{
		Root: filepath.Join(os.Getenv("programfiles"), "containerd", "root"),
		GRPC: server.GRPCConfig{
			Address: `\\.\pipe\containerd-containerd`,
		},
		Debug: server.Debug{
			Level:   "info",
			Address: `\\.\pipe\containerd-debug`,
		},
		Snapshotter: "io.containerd.snapshotter.v1.windows",
		Differ:      "io.containerd.differ.v1.base-diff",
	}
}
