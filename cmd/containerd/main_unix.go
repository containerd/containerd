// +build darwin freebsd

package main

import (
	"context"
	"os"

	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/reaper"
	"github.com/containerd/containerd/server"
)

const defaultConfigPath = "/etc/containerd/config.toml"

var handledSignals = []os.Signal{
	unix.SIGTERM,
	unix.SIGINT,
	unix.SIGUSR1,
	unix.SIGCHLD,
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
