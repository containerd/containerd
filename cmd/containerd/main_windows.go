package main

import (
	"context"
	"os"
	"path/filepath"
	"syscall"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/server"
)

var (
	defaultConfigPath = filepath.Join(os.Getenv("programfiles"), "containerd", "config.toml")
	handledSignals    = []os.Signal{
		syscall.SIGTERM,
		syscall.SIGINT,
	}
)

func handleSignals(ctx context.Context, signals chan os.Signal, server *server.Server) error {
	for s := range signals {
		log.G(ctx).WithField("signal", s).Debug("received signal")
		server.Stop()
	}
	return nil
}
