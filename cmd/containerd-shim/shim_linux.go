package main

import (
	"os"
	"os/signal"

	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/reaper"
	"github.com/containerd/containerd/sys"
	runc "github.com/containerd/go-runc"
)

// setupSignals creates a new signal handler for all signals and sets the shim as a
// sub-reaper so that the container processes are reparented
func setupSignals() (chan os.Signal, error) {
	signals := make(chan os.Signal, 2048)
	signal.Notify(signals)
	// make sure runc is setup to use the monitor
	// for waiting on processes
	runc.Monitor = reaper.Default
	// set the shim as the subreaper for all orphaned processes created by the container
	if err := sys.SetSubreaper(1); err != nil {
		return nil, err
	}
	return signals, nil
}

// setupRoot sets up the root as the shim is started in its own mount namespace
func setupRoot() error {
	return unix.Mount("", "/", "", unix.MS_SLAVE|unix.MS_REC, "")
}
