# protobuild

[![Build Status](https://github.com/containerd/protobuild/workflows/CI/badge.svg)](https://github.com/containerd/protobuild/actions?query=workflow%3ACI)
[![Go Report Card](https://goreportcard.com/badge/github.com/containerd/protobuild)](https://goreportcard.com/report/github.com/containerd/protobuild)

Build protobufs in Go, easily.

`protobuild` works by scanning the go package in a project and emitting correct
`protoc` commands, configured with the plugins, packages and details of your
choice.

The main benefit is that it makes it much easier to consume external types from
vendored projects. By integrating the protoc include paths with Go's vendoring
and GOPATH, builds are much easier to keep consistent across a project.

This comes from experience with generating protobufs with `go generate` in
swarmkit and the tool used with containerd. It should replace both.

## Status

Very early stages.

## Installation

To ensure easy use with builds, we'll try to support `go get`. Install with the
following command:

```
go get -u github.com/containerd/protobuild
```

## Usage

Protobuild works by providing a list of Go packages in which to build the
protobufs. To get started with a project, you must do the following:

1. Create a `Protobuild.toml` file in the root of your Go project. Use the
   [example](Protobuild.toml) as a starting point.

2. Make sure that the packages where you want your protobuf files have a Go
   file in place. Usually, adding a `doc.go` file is sufficient. A package for
   protobuf should look like this:

   ```
   foo.proto
   doc.go
   ```

   Where the contents of `doc.go` will have a package declaration in it. See
   the [example](examples/foo/doc.go) for details. Make sure the package name
   corresponds to what is in the `go_package` option.

3. Run the `protobuild` command:
    ```
    go list ./... | grep -v vendor | xargs protobuild
    ```

## Version Compatibility

Originally protoc-gen-go was supporting gRPC through its plugins mechanism.
[However gRPC support is later extracted as protoc-gen-go-gprc binary](https://github.com/protocolbuffers/protobuf-go/releases/tag/v1.20.0#user-content-v1.20-grpc-support).

To use protoc-gen-go and protoc-gen-go-grpc. Please specify `version = "2"` and
both code generators instead of `plugins` like below.

```
version = "2"
generators = ["go", "go-grpc"]
```

## Project details

protobuild is a containerd sub-project, licensed under the [Apache 2.0 license](./LICENSE).
As a containerd sub-project, you will find the:
 * [Project governance](https://github.com/containerd/project/blob/master/GOVERNANCE.md),
 * [Maintainers](https://github.com/containerd/project/blob/master/MAINTAINERS),
 * and [Contributing guidelines](https://github.com/containerd/project/blob/master/CONTRIBUTING.md)

information in our [`containerd/project`](https://github.com/containerd/project) repository.
