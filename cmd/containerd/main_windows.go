package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/server"

	"golang.org/x/sys/windows"
)

var (
	defaultConfigPath = filepath.Join(os.Getenv("programfiles"), "containerd", "config.toml")
	handledSignals    = []os.Signal{
		windows.SIGTERM,
		windows.SIGINT,
	}
)

func handleSignals(ctx context.Context, signals chan os.Signal, server *server.Server) error {
	for s := range signals {
		log.G(ctx).WithField("signal", s).Debug("received signal")
		server.Stop()
	}
	return nil
}
