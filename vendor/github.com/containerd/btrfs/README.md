# go-btrfs
[![GoDoc](https://godoc.org/github.com/containerd/btrfs?status.svg)](https://godoc.org/github.com/containerd/btrfs)
[![Build Status](https://travis-ci.org/containerd/btrfs.svg?branch=master)](https://travis-ci.org/containerd/btrfs)

Native Go bindings for btrfs.

# Status

These are in the early stages. We will try to maintain stability, but please
vendor if you are relying on these directly.

# Contribute

This package may not cover all the use cases for btrfs. If something you need
is missing, please don't hesitate to submit a PR.

Note that due to struct alignment issues, this isn't yet fully native.
Preferably, this could be resolved, so contributions in this direction are
greatly appreciated.

## Applying License Header to New Files

If you submit a contribution that adds a new file, please add the license
header. You can do so manually or use the `ltag` tool:


```console
$ go get github.com/kunalkushwaha/ltag
$ ltag -t ./license-templates
```

The above will add the appropriate licenses to Go files. New templates will
need to be added if other kinds of files are added. Please consult the
documentation at https://github.com/kunalkushwaha/ltag

## Project details

btrfs is a containerd sub-project, licensed under the [Apache 2.0 license](./LICENSE).
As a containerd sub-project, you will find the:
 * [Project governance](https://github.com/containerd/project/blob/master/GOVERNANCE.md),
 * [Maintainers](https://github.com/containerd/project/blob/master/MAINTAINERS),
 * and [Contributing guidelines](https://github.com/containerd/project/blob/master/CONTRIBUTING.md)

information in our [`containerd/project`](https://github.com/containerd/project) repository.
