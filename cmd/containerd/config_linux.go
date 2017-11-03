package main

import (
	"github.com/containerd/containerd/server"
	"github.com/urfave/cli"
)

var commands = []cli.Command{
	configCommand,
	shimCommand,
}

func defaultConfig() *server.Config {
	return &server.Config{
		Root:  server.DefaultRootDir,
		State: server.DefaultStateDir,
		GRPC: server.GRPCConfig{
			Address: server.DefaultAddress,
		},
		Subreaper: true,
		Debug: server.Debug{
			Level:   "info",
			Address: server.DefaultDebugAddress,
		},
	}
}
