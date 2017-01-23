package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/execution"
	"github.com/urfave/cli"
)

func appendPlatformFlags(flags []cli.Flag) []cli.Flag {
	return flags
}

func processRuntime(ctx context.Context, runtime string, root string) (execution.Executor, error) {
	// TODO
	return nil, nil
}

func setupSignals(signals chan os.Signal) {
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
}

func handleSignals(signals chan os.Signal, server *grpc.Server) {
	for s := range signals {
		switch s {
		default:
			logrus.WithField("signal", s).Info("containerd: stopping GRPC server")
			server.Stop()
			return
		}
	}
}
func createListener(path string) (net.Listener, error) {
	// TODO
	return nil, nil
}
