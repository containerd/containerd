// +build !windows

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"google.golang.org/grpc"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/execution"
	"github.com/docker/containerd/execution/executors/shim"
	"github.com/docker/containerd/log"
	"github.com/urfave/cli"
)

func appendPlatformFlags(flags []cli.Flag) []cli.Flag {
	return append(flags, cli.StringFlag{
		Name:  "socket, s",
		Usage: "socket path for containerd's GRPC server",
		Value: "/run/containerd/containerd.sock",
	})
}

func processRuntime(ctx context.Context, runtime string, root string) (execution.Executor, error) {
	var (
		err      error
		executor execution.Executor
	)
	switch runtime {
	case "shim":
		root := filepath.Join(root, "shim")
		err = os.Mkdir(root, 0700)
		if err != nil && !os.IsExist(err) {
			return nil, err
		}
		executor, err = shim.New(log.WithModule(ctx, "shim"), root, "containerd-shim", "runc", nil)
		if err != nil {
			return nil, err
		}
		return executor, nil
	default:
		return nil, fmt.Errorf("oci: runtime %q not implemented", runtime)
	}
}

func setupSignals(signals chan os.Signal) {
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1)
}

func handleSignals(signals chan os.Signal, server *grpc.Server) {
	for s := range signals {
		switch s {
		case syscall.SIGUSR1:
			dumpStacks()
		default:
			logrus.WithField("signal", s).Info("containerd: stopping GRPC server")
			server.Stop()
			return
		}
	}
}

func createListener(path string) (net.Listener, error) {
	if path == "" {
		return nil, fmt.Errorf("--socket path cannot be empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0660); err != nil {
		return nil, err
	}
	if err := syscall.Unlink(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return net.Listen("unix", path)
}
