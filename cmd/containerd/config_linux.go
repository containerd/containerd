package main

import (
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/server"
)

func defaultConfig() *server.Config {
	return &server.Config{
		Root: "/var/lib/containerd",
		GRPC: server.GRPCConfig{
			Address: containerd.DefaultAddress,
		},
		Debug: server.Debug{
			Level:   "info",
			Address: "/run/containerd/debug.sock",
		},
	}
}
