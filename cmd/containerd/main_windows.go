package main

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/containerd/containerd/log"
	"github.com/urfave/cli"
	"google.golang.org/grpc"
)

var (
	defaultConfigPath = filepath.Join(os.Getenv("programfiles"), "containerd", "config.toml")
	handledSignals    = []os.Signal{syscall.SIGTERM, syscall.SIGINT}
)

func platformInit(context *cli.Context) error {
	return nil
}

func handleSignals(signals chan os.Signal, server *grpc.Server) error {
	for s := range signals {
		log.G(global).WithField("signal", s).Debug("received signal")
		server.Stop()
	}
	return nil
}
