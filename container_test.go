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

package containerd

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	// Register the typeurl
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	_ "github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/runtime/v2/runc/options"
	"github.com/containerd/typeurl"
	specs "github.com/opencontainers/runtime-spec/specs-go"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/go-runc"
	gogotypes "github.com/gogo/protobuf/types"
)

func empty() cio.Creator {
	// TODO (@mlaventure) windows searches for pipes
	// when none are provided
	if runtime.GOOS == "windows" {
		return cio.NewCreator(cio.WithStdio)
	}
	return cio.NullIO
}

func TestContainerList(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext(t)
	defer cancel()

	containers, err := client.Containers(ctx)
	if err != nil {
		t.Fatalf("container list returned error %v", err)
	}
	if len(containers) != 0 {
		t.Errorf("expected 0 containers but received %d", len(containers))
	}
}

func TestNewContainer(t *testing.T) {
	t.Parallel()

	id := t.Name()
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext(t)
	defer cancel()

	container, err := client.NewContainer(ctx, id, WithNewSpec())
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx)
	if container.ID() != id {
		t.Errorf("expected container id %q but received %q", id, container.ID())
	}
	if _, err = container.Spec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := container.Delete(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestContainerStart(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), withExitStatus(7)))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "windows" {
		// task.Pid not implemented on Windows
		if pid := task.Pid(); pid < 1 {
			t.Errorf("invalid task pid %d", pid)
		}
	}

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		task.Delete(ctx)
		return
	}
	status := <-statusC
	code, _, err := status.Result()
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Errorf("expected status 7 from wait but received %d", code)
	}

	deleteStatus, err := task.Delete(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ec := deleteStatus.ExitCode(); ec != 7 {
		t.Errorf("expected status 7 from delete but received %d", ec)
	}
}

func TestContainerOutput(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
		expected    = "kingkoye"
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), withProcessArgs("echo", expected)))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	stdout := bytes.NewBuffer(nil)
	task, err := container.NewTask(ctx, cio.NewCreator(withByteBuffers(stdout)))
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	status := <-statusC
	code, _, err := status.Result()
	if code != 0 {
		t.Errorf("expected status 0 but received %d: %v", code, err)
	}
	if _, err := task.Delete(ctx); err != nil {
		t.Fatal(err)
	}

	actual := stdout.String()
	// echo adds a new line
	expected = expected + newLine
	if actual != expected {
		t.Errorf("expected output %q but received %q", expected, actual)
	}
}

func withByteBuffers(stdout io.Writer) cio.Opt {
	// TODO: could this use ioutil.Discard?
	return func(streams *cio.Streams) {
		streams.Stdin = new(bytes.Buffer)
		streams.Stdout = stdout
		streams.Stderr = new(bytes.Buffer)
	}
}

func TestContainerExec(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), longCommand))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	finishedC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}
	spec, err := container.Spec(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// start an exec process without running the original container process info
	processSpec := spec.Process
	withExecExitStatus(processSpec, 6)
	execID := t.Name() + "_exec"
	process, err := task.Exec(ctx, execID, processSpec, empty())
	if err != nil {
		t.Fatal(err)
	}
	processStatusC, err := process.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := process.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// wait for the exec to return
	status := <-processStatusC
	code, _, err := status.Result()
	if err != nil {
		t.Fatal(err)
	}

	if code != 6 {
		t.Errorf("expected exec exit code 6 but received %d", code)
	}
	deleteStatus, err := process.Delete(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ec := deleteStatus.ExitCode(); ec != 6 {
		t.Errorf("expected delete exit code 6 but received %d", ec)
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
	}
	<-finishedC
}
func TestContainerLargeExecArgs(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), longCommand))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	finishedC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}
	spec, err := container.Spec(ctx)
	if err != nil {
		t.Fatal(err)
	}

	processSpec := spec.Process
	withExecArgs(processSpec, "echo", strings.Repeat("a", 20000))
	execID := t.Name() + "_exec"
	process, err := task.Exec(ctx, execID, processSpec, empty())
	if err != nil {
		t.Fatal(err)
	}
	processStatusC, err := process.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := process.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// wait for the exec to return
	status := <-processStatusC
	if _, _, err := status.Result(); err != nil {
		t.Fatal(err)
	}
	if _, err := process.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
	}
	<-finishedC
}

func TestContainerPids(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), longCommand))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	pid := task.Pid()
	if pid < 1 {
		t.Errorf("invalid task pid %d", pid)
	}
	processes, err := task.Pids(ctx)
	switch runtime.GOOS {
	case "windows":
		// TODO: This is currently not implemented on windows
	default:
		if err != nil {
			t.Fatal(err)
		}
		// 2 processes, 1 for sh and one for sleep
		if l := len(processes); l != 2 {
			t.Errorf("expected 2 process but received %d", l)
		}
		if len(processes) > 0 {
			actual := processes[0].Pid
			if pid != actual {
				t.Errorf("expected pid %d but received %d", pid, actual)
			}
		}
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		select {
		case s := <-statusC:
			t.Log(s.Result())
		default:
		}
		t.Error(err)
	}
	<-statusC
}

func TestContainerCloseIO(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), withCat()))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	stdout := bytes.NewBuffer(nil)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	task, err := container.NewTask(ctx, cio.NewCreator(cio.WithStreams(r, stdout, ioutil.Discard)))
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := task.CloseIO(ctx, WithStdinCloser); err != nil {
		t.Error(err)
	}

	<-statusC
}

func TestDeleteRunningContainer(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), longCommand))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	err = container.Delete(ctx, WithSnapshotCleanup)
	if err == nil {
		t.Error("delete did not error with running task")
	}
	if !errdefs.IsFailedPrecondition(err) {
		t.Errorf("expected error %q but received %q", errdefs.ErrFailedPrecondition, err)
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-statusC
}

func TestContainerKill(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), longCommand))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-statusC

	err = task.Kill(ctx, syscall.SIGTERM)
	if err == nil {
		t.Fatal("second call to kill should return an error")
	}
	if !errdefs.IsNotFound(err) {
		t.Errorf("expected error %q but received %q", errdefs.ErrNotFound, err)
	}
}

func TestKillContainerDeletedByRunc(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("Test relies on runc and is not supported on Windows")
	}

	// We skip this case when runtime is crun.
	// More information in https://github.com/containerd/containerd/pull/4214#discussion_r422769497
	if os.Getenv("RUNC_FLAVOR") == "crun" {
		t.Skip("skip it when using crun")
	}

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if client.runtime == plugin.RuntimeLinuxV1 {
		t.Skip("test relies on runtime v2")
	}

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
		runcRoot    = "/tmp/runc-test"
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), longCommand),
		WithRuntime(client.runtime, &options.Options{Root: runcRoot}))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	rcmd := &runc.Runc{
		Root: path.Join(runcRoot, testNamespace),
	}

	if err := rcmd.Delete(ctx, id, &runc.DeleteOpts{Force: true}); err != nil {
		t.Fatal(err)
	}
	err = task.Kill(ctx, syscall.SIGKILL)
	if err == nil {
		t.Fatal("kill should return NotFound error")
	} else if !errdefs.IsNotFound(err) {
		t.Errorf("expected error %q but received %q", errdefs.ErrNotFound, err)
	}

	select {
	case <-statusC:
	case <-time.After(2 * time.Second):
		t.Errorf("unexpected timeout when try to get exited container's status")
	}
}

func TestContainerNoBinaryExists(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), oci.WithProcessArgs("nothing")))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	switch runtime.GOOS {
	case "windows":
		if err != nil {
			t.Fatalf("failed to create task %v", err)
		}
		defer task.Delete(ctx, WithProcessKill)
		if err := task.Start(ctx); err == nil {
			t.Error("task.Start() should return an error when binary does not exist")
		}
	default:
		if err == nil {
			t.Error("NewTask should return an error when binary does not exist")
			task.Delete(ctx)
		}
	}
}

func TestContainerExecNoBinaryExists(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), longCommand))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	finishedC, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
	}
	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}
	spec, err := container.Spec(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// start an exec process without running the original container process
	processSpec := spec.Process
	processSpec.Args = []string{
		"none",
	}
	execID := t.Name() + "_exec"
	process, err := task.Exec(ctx, execID, processSpec, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer process.Delete(ctx)
	if err := process.Start(ctx); err == nil {
		t.Error("Process.Start should fail when process does not exist")
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
	}
	<-finishedC
}

func TestWaitStoppedTask(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), withExitStatus(7)))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "windows" {
		// Getting the pid is not currently implemented on windows
		if pid := task.Pid(); pid < 1 {
			t.Errorf("invalid task pid %d", pid)
		}
	}
	if err := task.Start(ctx); err != nil {
		t.Error(err)
		task.Delete(ctx)
		return
	}

	// wait for the task to stop then call wait again
	<-statusC
	statusC, err = task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	status := <-statusC
	code, _, err := status.Result()
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Errorf("exit status from stopped task should be 7 but received %d", code)
	}
}

func TestWaitStoppedProcess(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), longCommand))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	finishedC, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}
	spec, err := container.Spec(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// start an exec process without running the original container process info
	processSpec := spec.Process
	withExecExitStatus(processSpec, 6)
	execID := t.Name() + "_exec"
	process, err := task.Exec(ctx, execID, processSpec, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer process.Delete(ctx)

	statusC, err := process.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := process.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// wait for the exec to return
	<-statusC

	// try to wait on the process after it has stopped
	statusC, err = process.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	status := <-statusC
	code, _, err := status.Result()
	if err != nil {
		t.Fatal(err)
	}
	if code != 6 {
		t.Errorf("exit status from stopped process should be 6 but received %d", code)
	}

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
	}
	<-finishedC
}

func TestTaskForceDelete(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), longCommand))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := task.Delete(ctx); err == nil {
		t.Error("task.Delete of a running task should create an error")
	}
	if _, err := task.Delete(ctx, WithProcessKill); err != nil {
		t.Fatal(err)
	}
}

func TestProcessForceDelete(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), longCommand))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// task must be started on windows
	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}
	spec, err := container.Spec(ctx)
	if err != nil {
		t.Fatal(err)
	}

	processSpec := spec.Process
	if runtime.GOOS == "windows" {
		withExecArgs(processSpec, "cmd", "/c", "ping -t localhost")
	} else {
		withExecArgs(processSpec, "/bin/sh", "-c", "while true; do sleep 1; done")
	}
	execID := t.Name() + "_exec"
	process, err := task.Exec(ctx, execID, processSpec, empty())
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := process.Delete(ctx); err == nil {
		t.Error("process.Delete should return an error when process is running")
	}
	if _, err := process.Delete(ctx, WithProcessKill); err != nil {
		t.Error(err)
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-statusC
}

func TestContainerHostname(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
		expected    = "myhostname"
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image),
		withProcessArgs("hostname"),
		oci.WithHostname(expected),
	))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	stdout := bytes.NewBuffer(nil)
	task, err := container.NewTask(ctx, cio.NewCreator(withByteBuffers(stdout)))
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	status := <-statusC
	code, _, err := status.Result()
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Errorf("expected status 0 but received %d", code)
	}
	if _, err := task.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	cutset := "\n"
	if runtime.GOOS == "windows" {
		cutset = "\r\n"
	}

	actual := strings.TrimSuffix(stdout.String(), cutset)
	if actual != expected {
		t.Errorf("expected output %q but received %q", expected, actual)
	}
}

func TestContainerExitedAtSet(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), withTrue()))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
	}

	startTime := time.Now()
	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	status := <-statusC
	code, _, err := status.Result()
	if code != 0 {
		t.Errorf("expected status 0 but received %d (err: %v)", code, err)
	}

	if s, err := task.Status(ctx); err != nil {
		t.Errorf("failed to retrieve status: %v", err)
	} else if s.ExitTime.After(startTime) == false {
		t.Errorf("exit time is not after start time: %v <= %v", startTime, s.ExitTime)
	}

	if _, err := task.Delete(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteContainerExecCreated(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), longCommand))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	finished, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}
	spec, err := container.Spec(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// start an exec process without running the original container process info
	processSpec := spec.Process
	withExecExitStatus(processSpec, 6)
	execID := t.Name() + "_exec"
	process, err := task.Exec(ctx, execID, processSpec, empty())
	if err != nil {
		t.Fatal(err)
	}
	deleteStatus, err := process.Delete(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ec := deleteStatus.ExitCode(); ec != 0 {
		t.Errorf("expected delete exit code 0 but received %d", ec)
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
	}
	<-finished
}

func TestContainerMetrics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("metrics are currently not supported on windows")
	}
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), longCommand))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx, WithProcessKill)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	metric, err := task.Metrics(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	if metric.ID != id {
		t.Errorf("expected metric id %q but received %q", id, metric.ID)
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	<-statusC
}

func TestDeletedContainerMetrics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("metrics are currently not supported on windows")
	}
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), withExitStatus(0)))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	<-statusC

	if _, err := task.Delete(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := task.Metrics(ctx); err == nil {
		t.Errorf("Getting metrics of deleted task should have failed")
	}
}

func TestContainerExtensions(t *testing.T) {
	t.Parallel()

	ctx, cancel := testContext(t)
	defer cancel()
	id := t.Name()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ext := gogotypes.Any{TypeUrl: "test.ext.url", Value: []byte("hello")}
	container, err := client.NewContainer(ctx, id, WithNewSpec(), WithContainerExtension("hello", &ext))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx)

	checkExt := func(container Container) {
		cExts, err := container.Extensions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(cExts) != 1 {
			t.Errorf("expected 1 container extension")
		}
		if cExts["hello"].TypeUrl != ext.TypeUrl {
			t.Errorf("got unexpected type url for extension: %s", cExts["hello"].TypeUrl)
		}
		if !bytes.Equal(cExts["hello"].Value, ext.Value) {
			t.Errorf("expected extension value %q, got: %q", ext.Value, cExts["hello"].Value)
		}
	}

	checkExt(container)

	container, err = client.LoadContainer(ctx, container.ID())
	if err != nil {
		t.Fatal(err)
	}
	checkExt(container)
}

func TestContainerUpdate(t *testing.T) {
	t.Parallel()

	ctx, cancel := testContext(t)
	defer cancel()
	id := t.Name()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	container, err := client.NewContainer(ctx, id, WithNewSpec())
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx)

	spec, err := container.Spec(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const hostname = "updated-hostname"
	spec.Hostname = hostname

	if err := container.Update(ctx, func(ctx context.Context, client *Client, c *containers.Container) error {
		a, err := typeurl.MarshalAny(spec)
		if err != nil {
			return err
		}
		c.Spec = a
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if spec, err = container.Spec(ctx); err != nil {
		t.Fatal(err)
	}
	if spec.Hostname != hostname {
		t.Errorf("hostname %q != %q", spec.Hostname, hostname)
	}
}

func TestContainerInfo(t *testing.T) {
	t.Parallel()

	ctx, cancel := testContext(t)
	defer cancel()
	id := t.Name()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	container, err := client.NewContainer(ctx, id, WithNewSpec())
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx)

	info, err := container.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != container.ID() {
		t.Fatalf("info.ID=%s != container.ID()=%s", info.ID, container.ID())
	}
}

func TestContainerLabels(t *testing.T) {
	t.Parallel()

	ctx, cancel := testContext(t)
	defer cancel()
	id := t.Name()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	container, err := client.NewContainer(ctx, id, WithNewSpec(), WithContainerLabels(map[string]string{
		"test": "yes",
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx)

	labels, err := container.Labels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if labels["test"] != "yes" {
		t.Fatalf("expected label \"test\" to be \"yes\"")
	}
	labels["test"] = "no"
	if labels, err = container.SetLabels(ctx, labels); err != nil {
		t.Fatal(err)
	}
	if labels["test"] != "no" {
		t.Fatalf("expected label \"test\" to be \"no\"")
	}
}

func TestContainerHook(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	hook := func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
		if s.Hooks == nil {
			s.Hooks = &specs.Hooks{}
		}
		path, err := exec.LookPath("containerd")
		if err != nil {
			return err
		}
		psPath, err := exec.LookPath("ps")
		if err != nil {
			return err
		}
		s.Hooks.Prestart = []specs.Hook{
			{
				Path: path,
				Args: []string{
					"containerd",
					"oci-hook", "--",
					psPath, "--pid", "{{pid}}",
				},
				Env: os.Environ(),
			},
		}
		return nil
	}
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), hook))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx, WithProcessKill)
}

func TestShimSockLength(t *testing.T) {
	t.Parallel()

	// Max length of namespace should be 76
	namespace := strings.Repeat("n", 76)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx = namespaces.WithNamespace(ctx, namespace)

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	image, err := client.Pull(ctx, testImage,
		WithPlatformMatcher(platforms.Default()),
		WithPullUnpack,
	)
	if err != nil {
		t.Fatal(err)
	}

	id := strings.Repeat("c", 64)

	// We don't have limitation with length of container name,
	// but 64 bytes of sha256 is the common case
	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), withExitStatus(0)),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	<-statusC
}

func TestContainerExecLargeOutputWithTTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Test does not run on Windows")
	}

	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), longCommand))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	finishedC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 100; i++ {
		spec, err := container.Spec(ctx)
		if err != nil {
			t.Fatal(err)
		}

		// start an exec process without running the original container process info
		processSpec := spec.Process
		withExecArgs(processSpec, "sh", "-c", `seq -s " " 1000000`)

		stdout := bytes.NewBuffer(nil)

		execID := t.Name() + "_exec"
		process, err := task.Exec(ctx, execID, processSpec, cio.NewCreator(withByteBuffers(stdout), withProcessTTY()))
		if err != nil {
			t.Fatal(err)
		}
		processStatusC, err := process.Wait(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if err := process.Start(ctx); err != nil {
			t.Fatal(err)
		}

		// wait for the exec to return
		status := <-processStatusC
		code, _, err := status.Result()
		if err != nil {
			t.Fatal(err)
		}

		if code != 0 {
			t.Errorf("expected exec exit code 0 but received %d", code)
		}
		if _, err := process.Delete(ctx); err != nil {
			t.Fatal(err)
		}

		const expectedSuffix = "999999 1000000"
		stdoutString := stdout.String()
		if !strings.Contains(stdoutString, expectedSuffix) {
			t.Fatalf("process output does not end with %q at iteration %d, here are the last 20 characters of the output:\n\n %q", expectedSuffix, i, stdoutString[len(stdoutString)-20:])
		}

	}

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
	}
	<-finishedC
}

func TestShortRunningTaskPid(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), shortCommand))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	finishedC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	int32PID := int32(task.Pid())
	if int32PID <= 0 {
		t.Errorf("Unexpected task pid %d", int32PID)
	}
	<-finishedC
}

func withProcessTTY() cio.Opt {
	return func(opt *cio.Streams) {
		cio.WithTerminal(opt)
	}
}
