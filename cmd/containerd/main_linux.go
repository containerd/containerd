package main

import (
	"context"
	"os"

	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/reaper"
	"github.com/containerd/containerd/server"
	"github.com/containerd/containerd/sys"
)

const defaultConfigPath = "/etc/containerd/config.toml"

var handledSignals = []os.Signal{
	unix.SIGTERM,
	unix.SIGINT,
	unix.SIGUSR1,
	unix.SIGCHLD,
}

func platformInit(ctx context.Context, config *server.Config) error {
	if config.Subreaper {
		log.G(ctx).Info("setting subreaper...")
		if err := sys.SetSubreaper(1); err != nil {
			return err
		}
	}
	if config.OOMScore != 0 {
		log.G(ctx).Infof("changing OOM score to %d", config.OOMScore)
		if err := sys.SetOOMScore(os.Getpid(), config.OOMScore); err != nil {
			return err
		}
	}
	return nil
}

func handleSignals(ctx context.Context, signals chan os.Signal, server *server.Server) error {
	for s := range signals {
		log.G(ctx).WithField("signal", s).Debug("received signal")
		switch s {
		case unix.SIGCHLD:
			if err := reaper.Reap(); err != nil {
				log.G(ctx).WithError(err).Error("reap containerd processes")
			}
		case unix.SIGUSR1:
			dumpStacks()
		default:
			server.Stop()
			return nil
		}
	}
	return nil
}
