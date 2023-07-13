//go:build tools

package hcsshim

import (
	// for go generate directives

	// generate Win32 API code
	_ "github.com/Microsoft/go-winio/tools/mkwinsyscall"

	// mock gRPC client and servers
	_ "github.com/golang/mock/mockgen"
)
