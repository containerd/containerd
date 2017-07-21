// +build !linux,!windows

package server

import "context"

const (
	// DefaultAddress is the default unix socket address
	DefaultAddress = "/run/containerd/containerd.sock"
	// DefaultDebuggAddress is the default unix socket address for pprof data
	DefaultDebugAddress = "/run/containerd/debug.sock"
)

func apply(_ context.Context, _ *Config) error {
	return nil
}
