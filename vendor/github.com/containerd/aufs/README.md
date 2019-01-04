# aufs snapshotter

[![Build Status](https://travis-ci.org/containerd/aufs.svg?branch=master)](https://travis-ci.org/containerd/aufs)

[![codecov](https://codecov.io/gh/containerd/aufs/branch/master/graph/badge.svg)](https://codecov.io/gh/containerd/aufs)


AUFS implementation of the snapshot interface for containerd.

## Compile

To compile containerd with aufs support add the import into the `cmd/containerd/builtins_linux.go` file.

```go
package main

import (
	_ "github.com/containerd/aufs"
	_ "github.com/containerd/containerd/linux"
	_ "github.com/containerd/containerd/metrics/cgroups"
	_ "github.com/containerd/containerd/snapshot/overlay"
)
```

## Project details

aufs is a containerd sub-project, licensed under the [Apache 2.0 license](./LICENSE).
As a containerd sub-project, you will find the:
 * [Project governance](https://github.com/containerd/project/blob/master/GOVERNANCE.md),
 * [Maintainers](https://github.com/containerd/project/blob/master/MAINTAINERS),
 * and [Contributing guidelines](https://github.com/containerd/project/blob/master/CONTRIBUTING.md)

information in our [`containerd/project`](https://github.com/containerd/project) repository.
