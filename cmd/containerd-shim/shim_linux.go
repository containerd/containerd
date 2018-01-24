package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/containerd/containerd/reaper"
	runc "github.com/containerd/go-runc"
	"github.com/opencontainers/runc/libcontainer/system"
	"github.com/stevvooe/ttrpc"
)

// setupSignals creates a new signal handler for all signals and sets the shim as a
// sub-reaper so that the container processes are reparented
func setupSignals() (chan os.Signal, error) {
	signals := make(chan os.Signal, 32)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGCHLD)
	// make sure runc is setup to use the monitor
	// for waiting on processes
	runc.Monitor = reaper.Default
	// set the shim as the subreaper for all orphaned processes created by the container
	if err := system.SetSubreaper(1); err != nil {
		return nil, err
	}
	return signals, nil
}

func newServer() (*ttrpc.Server, error) {
	return ttrpc.NewServer(ttrpc.WithServerHandshaker(ttrpc.UnixSocketRequireSameUser()))
}
