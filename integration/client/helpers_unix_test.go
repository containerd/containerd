//go:build !windows
// +build !windows

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package client

import (
	"context"
	"fmt"
	"syscall"
	"testing"

	"github.com/containerd/cgroups"
	cgroupsv2 "github.com/containerd/cgroups/v2"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const newLine = "\n"

func withExitStatus(es int) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
		s.Process.Args = []string{"sh", "-c", fmt.Sprintf("exit %d", es)}
		return nil
	}
}

func withProcessArgs(args ...string) oci.SpecOpts {
	return oci.WithProcessArgs(args...)
}

func withCat() oci.SpecOpts {
	return oci.WithProcessArgs("cat")
}

func withTrue() oci.SpecOpts {
	return oci.WithProcessArgs("true")
}

func withExecExitStatus(s *specs.Process, es int) {
	s.Args = []string{"sh", "-c", fmt.Sprintf("exit %d", es)}
}

func withExecArgs(s *specs.Process, args ...string) {
	s.Args = args
}

func kill(pid int, signal syscall.Signal) error {
	return syscall.Kill(pid, signal)
}

func newDirectIO(ctx context.Context, terminal bool) (*directIO, error) {
	fifos, err := cio.NewFIFOSetInDir("", "", terminal)
	if err != nil {
		return nil, err
	}
	dio, err := cio.NewDirectIO(ctx, fifos)
	if err != nil {
		return nil, err
	}
	return &directIO{DirectIO: *dio}, nil
}

func checkTaskMemoryUsage(t *testing.T, task containerd.Task, expectedLimit int64) {
	if cgroups.Mode() == cgroups.Unified {
		groupPath, err := cgroupsv2.PidGroupPath(int(task.Pid()))
		if err != nil {
			t.Fatal(err)
		}
		cgroup2, err := cgroupsv2.LoadManager("/sys/fs/cgroup", groupPath)
		if err != nil {
			t.Fatal(err)
		}
		stat, err := cgroup2.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if int64(stat.Memory.UsageLimit) != expectedLimit {
			t.Fatalf("expected memory limit to be set to %d but received %d", expectedLimit, stat.Memory.UsageLimit)
		}
	} else {
		cgroup, err := cgroups.Load(cgroups.V1, cgroups.PidPath(int(task.Pid())))
		if err != nil {
			t.Fatal(err)
		}
		stat, err := cgroup.Stat(cgroups.IgnoreNotExist)
		if err != nil {
			t.Fatal(err)
		}
		if int64(stat.Memory.Usage.Limit) != expectedLimit {
			t.Fatalf("expected memory limit to be set to %d but received %d", expectedLimit, stat.Memory.Usage.Limit)
		}
	}
}
