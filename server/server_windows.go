// +build windows

package server

import "context"

const (
	// DefaultAddress is the default winpipe address
	DefaultAddress = `\\.\pipe\containerd-containerd`
	// DefaultDebugAddress is the default winpipe address for pprof data
	DefaultDebugAddress = `\\.\pipe\containerd-debug`
)

func apply(_ context.Context, _ *Config) error {
	return nil
}
