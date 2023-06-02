//go:build tools

package hcsshim

import (
	// protobuf generation
	_ "github.com/containerd/protobuild"
	_ "github.com/containerd/protobuild/cmd/go-fix-acronym"
	_ "github.com/containerd/ttrpc/cmd/protoc-gen-go-ttrpc"
	_ "google.golang.org/grpc/cmd/protoc-gen-go-grpc"
	_ "google.golang.org/protobuf/cmd/protoc-gen-go"

	// go generate
	_ "github.com/Microsoft/go-winio/tools/mkwinsyscall"
)
