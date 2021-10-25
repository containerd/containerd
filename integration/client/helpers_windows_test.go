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
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Microsoft/hcsshim"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	exec "golang.org/x/sys/execabs"
)

const newLine = "\r\n"

func withExitStatus(es int) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
		s.Process.Args = []string{"cmd", "/c", "exit", strconv.Itoa(es)}
		return nil
	}
}

func withProcessArgs(args ...string) oci.SpecOpts {
	return oci.WithProcessArgs(append([]string{"cmd", "/c"}, args...)...)
}

func withCat() oci.SpecOpts {
	return oci.WithProcessArgs("cmd", "/c", "more")
}

func withTrue() oci.SpecOpts {
	return oci.WithProcessArgs("cmd", "/c")
}

func withExecExitStatus(s *specs.Process, es int) {
	s.Args = []string{"cmd", "/c", "exit", strconv.Itoa(es)}
}

func withExecArgs(s *specs.Process, args ...string) {
	s.Args = append([]string{"cmd", "/c"}, args...)
}

func kill(pid int, signal syscall.Signal) error {
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	err := kill.Run()

	// Note: We return syscall.ESRCH if the process was not found, so we have the
	// same behaviour as unix.Kill in this regard.
	if err != nil && strings.Contains(err.Error(), " not found.") {
		return syscall.ESRCH
	}

	return err
}

type bytesBuffer struct {
	*bytes.Buffer
}

// Close is a noop operation.
func (b bytesBuffer) Close() error {
	return nil
}

func newDirectIO(ctx context.Context, terminal bool) (*directIO, error) {
	readb := bytesBuffer{bytes.NewBuffer(nil)}
	writeb := io.NopCloser(bytes.NewBuffer(nil))
	errb := io.NopCloser(bytes.NewBuffer(nil))

	dio := cio.NewDirectIO(readb, writeb, errb, terminal)
	return &directIO{DirectIO: *dio}, nil
}

func checkTaskMemoryUsage(t *testing.T, task containerd.Task, expectedLimit int64) {
	container, err := hcsshim.OpenContainer(task.ID())
	if err != nil {
		t.Fatal(err)
	}

	stats, err := container.Statistics()
	if err != nil {
		t.Fatal(err)
	}

	if stats.Memory.UsageCommitPeakBytes != uint64(expectedLimit) {
		t.Fatalf("expected memory limit to be set to %d but received %d", expectedLimit, stats.Memory.UsageCommitPeakBytes)
	}
}
