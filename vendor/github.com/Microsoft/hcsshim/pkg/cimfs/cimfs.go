//go:build windows
// +build windows

package cimfs

import "github.com/Microsoft/hcsshim/osversion"

func IsCimfsSupported() bool {
	return osversion.Get().Version >= MinimumCimFSBuild
}
