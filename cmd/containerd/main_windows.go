package main

import (
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	"github.com/containerd/containerd/log"
)

func notifySignals(signals chan os.Signal) {
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
}

func handleSignals(signals chan os.Signal, server *grpc.Server) error {
	for s := range signals {
		log.G(global).WithField("signal", s).Debug("received signal")
		switch s {
		default:
			server.Stop()
			return nil
		}
	}
	return nil
}

func configureReaper() error {
	return nil
}
