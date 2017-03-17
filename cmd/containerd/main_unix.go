// +build !windows

package main

import (
	"os"
	"syscall"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/reaper"
	"github.com/containerd/containerd/sys"
	"github.com/urfave/cli"
	"google.golang.org/grpc"
)

const (
	defaultConfigPath = "/etc/containerd/config.toml"
)

var (
	handledSignals = []os.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1, syscall.SIGCHLD}
)

func platformInit(context *cli.Context) error {
	if conf.Subreaper {
		log.G(global).Info("setting subreaper...")
		if err := sys.SetSubreaper(1); err != nil {
			return err
		}
	}
	return nil
}

func handleSignals(signals chan os.Signal, server *grpc.Server) error {
	for s := range signals {
		log.G(global).WithField("signal", s).Debug("received signal")
		switch s {
		case syscall.SIGCHLD:
			if err := reaper.Reap(); err != nil {
				log.G(global).WithError(err).Error("reap containerd processes")
			}
		default:
			server.Stop()
			return nil
		}
	}
	return nil
}
