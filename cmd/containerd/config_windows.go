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
			Address: server.DefaultAddress,
		},
		Debug: server.Debug{
			Level:   "info",
			Address: server.DefaultDebugAddress,
		},
	}
}
