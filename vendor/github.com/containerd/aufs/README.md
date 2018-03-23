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
