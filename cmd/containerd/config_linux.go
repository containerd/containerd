package main

import (
	"github.com/containerd/containerd/defaults"
	"github.com/containerd/containerd/server"
	"github.com/urfave/cli"
)

var commands = []cli.Command{
	configCommand,
	shimCommand,
}

func defaultConfig() *server.Config {
	return &server.Config{
		Root:  defaults.DefaultRootDir,
		State: defaults.DefaultStateDir,
		GRPC: server.GRPCConfig{
			Address: defaults.DefaultAddress,
		},
		Subreaper: true,
		Debug: server.Debug{
			Level:   "info",
			Address: defaults.DefaultDebugAddress,
		},
	}
}
