// +build solaris

package main

import "github.com/containerd/containerd/server"

func defaultConfig() *server.Config {
	return &server.Config{
		Root: "/var/lib/containerd",
		GRPC: server.GRPCConfig{
			Address: server.DefaultAddress,
		},
		Debug: server.Debug{
			Level:   "info",
			Address: server.DefaultDebugAddress,
		},
	}
}
