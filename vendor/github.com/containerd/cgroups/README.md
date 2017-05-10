# cgroups

[![Build Status](https://travis-ci.org/containerd/cgroups.svg?branch=master)](https://travis-ci.org/containerd/cgroups)

[![codecov](https://codecov.io/gh/containerd/cgroups/branch/master/graph/badge.svg)](https://codecov.io/gh/containerd/cgroups)

Go package for creating, managing, inspecting, and destroying cgroups.
The resources format for settings on the cgroup uses the OCI runtime-spec found
[here](https://github.com/opencontainers/runtime-spec).

## Examples

### Create a new cgroup

This creates a new cgroup using a static path for all subsystems under `/test`.

* /sys/fs/cgroup/cpu/test
* /sys/fs/cgroup/memory/test
* etc....

It uses a single hierarchy and specifies cpu shares as a resource constraint and
uses the v1 implementation of cgroups.


```go
shares := uint64(100)
control, err := cgroups.New(cgroups.V1, cgroups.StaticPath("/test"), &specs.LinuxResources{
    CPU: &specs.CPU{
        Shares: &shares,
    },
})
defer control.Delete()
```

### Create with systemd slice support


```go
control, err := cgroups.New(cgroups.Systemd, cgroups.Slice("system.slice", "runc-test"), &specs.LinuxResources{
    CPU: &specs.CPU{
        Shares: &shares,
    },
})

```

### Load an existing cgroup

```go
control, err = cgroups.Load(cgroups.V1, cgroups.StaticPath("/test"))
```

### Add a process to the cgroup

```go
if err := control.Add(cgroups.Process{Pid:1234}); err != nil {
}
```

###  Update the cgroup 

To update the resources applied in the cgroup

```go
shares = uint64(200)
if err := control.Update(&specs.LinuxResources{
    CPU: &specs.CPU{
        Shares: &shares,
    },
}); err != nil {
}
```

### Freeze and Thaw the cgroup

```go
if err := control.Freeze(); err != nil {
}
if err := control.Thaw(); err != nil {
}
```

### List all processes in the cgroup or recursively

```go
processes, err := control.Processes(cgroups.Devices, recursive)
```

### Get Stats on the cgroup

```go
stats, err := control.Stat()
```

### Move process across cgroups

This allows you to take processes from one cgroup and move them to another.

```go
err := control.MoveTo(destination)
```

### Create subcgroup

```go
subCgroup, err := control.New("child", resources)
```

## LICENSE

Copyright (c) 2016-2017 Michael Crosby. crosbymichael@gmail.com

Permission is hereby granted, free of charge, to any person
obtaining a copy of this software and associated documentation 
files (the "Software"), to deal in the Software without 
restriction, including without limitation the rights to use, copy, 
modify, merge, publish, distribute, sublicense, and/or sell copies 
of the Software, and to permit persons to whom the Software is 
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be 
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED,
INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, 
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. 
IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT 
HOLDERS BE LIABLE FOR ANY CLAIM, 
DAMAGES OR OTHER LIABILITY, 
WHETHER IN AN ACTION OF CONTRACT, 
TORT OR OTHERWISE, 
ARISING FROM, OUT OF OR IN CONNECTION WITH 
THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

