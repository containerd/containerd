//go:build linux

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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	. "github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/oci"
)

const (
	testCheckpointName = "checkpoint-test:latest"
)

func TestCheckpointRestorePTY(t *testing.T) {
	if !supportsCriu {
		t.Skip("system does not have criu installed")
	}
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image),
			oci.WithProcessArgs("sh", "-c", "read A; echo z${A}z"),
			oci.WithTTY),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	direct, err := newDirectIO(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Delete()

	task, err := container.NewTask(ctx, direct.IOCreate)
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

	checkpoint, err := container.Checkpoint(ctx, testCheckpointName+"withpty", []CheckpointOpts{
		WithCheckpointRuntime,
		WithCheckpointRW,
		WithCheckpointTaskExit,
		WithCheckpointTask,
	}...)
	if err != nil {
		t.Fatal(err)
	}

	<-statusC

	if _, err := task.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	direct.Delete()
	if err := container.Delete(ctx, WithSnapshotCleanup); err != nil {
		t.Fatal(err)
	}

	direct, err = newDirectIO(ctx, true)
	if err != nil {
		t.Fatal(err)
	}

	var (
		wg  sync.WaitGroup
		buf = bytes.NewBuffer(nil)
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(buf, direct.Stdout)
	}()

	if container, err = client.Restore(ctx, id, checkpoint, []RestoreOpts{
		WithRestoreImage,
		WithRestoreSpec,
		WithRestoreRuntime,
		WithRestoreRW,
	}...); err != nil {
		t.Fatal(err)
	}
	if task, err = container.NewTask(ctx, direct.IOCreate,
		WithTaskCheckpoint(checkpoint)); err != nil {
		t.Fatal(err)
	}

	statusC, err = task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	direct.Stdin.Write([]byte("hello\n"))
	<-statusC
	wg.Wait()

	if err := direct.Close(); err != nil {
		t.Error(err)
	}

	out := buf.String()
	if !strings.Contains(fmt.Sprintf("%#q", out), `zhelloz`) {
		t.Fatalf(`expected \x00 in output: %s`, out)
	}
}

func TestCheckpointRestore(t *testing.T) {
	if !supportsCriu {
		t.Skip("system does not have criu installed")
	}
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), oci.WithProcessArgs("sleep", "10")))
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

	checkpoint, err := container.Checkpoint(ctx, testCheckpointName+"restore", []CheckpointOpts{
		WithCheckpointRuntime,
		WithCheckpointRW,
		WithCheckpointTask,
	}...)
	if err != nil {
		t.Fatal(err)
	}

	<-statusC

	if _, err := task.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if err := container.Delete(ctx, WithSnapshotCleanup); err != nil {
		t.Fatal(err)
	}

	if container, err = client.Restore(ctx, id, checkpoint, []RestoreOpts{
		WithRestoreImage,
		WithRestoreSpec,
		WithRestoreRuntime,
		WithRestoreRW,
	}...); err != nil {
		t.Fatal(err)
	}
	if task, err = container.NewTask(ctx, empty(), WithTaskCheckpoint(checkpoint)); err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err = task.Wait(ctx)
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
}

func TestCheckpointRestoreNewContainer(t *testing.T) {
	if !supportsCriu {
		t.Skip("system does not have criu installed")
	}
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	id := t.Name()
	ctx, cancel := testContext(t)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), oci.WithProcessArgs("sleep", "5")))
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

	checkpoint, err := container.Checkpoint(ctx, testCheckpointName+"newcontainer", []CheckpointOpts{
		WithCheckpointRuntime,
		WithCheckpointRW,
		WithCheckpointTask,
	}...)
	if err != nil {
		t.Fatal(err)
	}

	<-statusC

	if _, err := task.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if err := container.Delete(ctx, WithSnapshotCleanup); err != nil {
		t.Fatal(err)
	}
	if container, err = client.Restore(ctx, id, checkpoint, []RestoreOpts{
		WithRestoreImage,
		WithRestoreSpec,
		WithRestoreRuntime,
		WithRestoreRW,
	}...); err != nil {
		t.Fatal(err)
	}
	if task, err = container.NewTask(ctx, empty(), WithTaskCheckpoint(checkpoint)); err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err = task.Wait(ctx)
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
}

func TestCheckpointLeaveRunning(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	if !supportsCriu {
		t.Skip("system does not have criu installed")
	}
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), oci.WithProcessArgs("sleep", "100")))
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

	// checkpoint
	if _, err := container.Checkpoint(ctx, testCheckpointName+"leaverunning", []CheckpointOpts{
		WithCheckpointRuntime,
		WithCheckpointRW,
		WithCheckpointTask,
	}...); err != nil {
		t.Fatal(err)
	}

	status, err := task.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != Running {
		t.Fatalf("expected status %q but received %q", Running, status)
	}

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	<-statusC
}

func TestCheckpointRestoreWithImagePath(t *testing.T) {
	if !supportsCriu {
		t.Skip("system does not have criu installed")
	}

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		ctx, cancel = testContext(t)
		id          = t.Name() + "-checkpoint"
	)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), oci.WithProcessArgs("top")))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// create image path store criu image files
	crDir := t.TempDir()
	imagePath := filepath.Join(crDir, "cr")
	// checkpoint task
	if _, err := task.Checkpoint(ctx, WithCheckpointImagePath(imagePath)); err != nil {
		t.Fatal(err)
	}

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-statusC
	task.Delete(ctx)

	// check image files have been dumped into image path
	if files, err := os.ReadDir(imagePath); err != nil || len(files) == 0 {
		t.Fatal("failed to checkpoint with image path set")
	}

	// restore task with same container image and checkpoint directory,
	// the restore process should finish in millisecond level
	id = t.Name() + "-restore"
	ncontainer, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image)))
	if err != nil {
		t.Fatal(err)
	}
	defer ncontainer.Delete(ctx, WithSnapshotCleanup)

	ntask, err := ncontainer.NewTask(ctx, empty(), WithRestoreImagePath(imagePath))
	if err != nil {
		t.Fatal(err)
	}
	statusC, err = ntask.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := ntask.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// check top process is existed in restored container
	spec, err := container.Spec(ctx)
	if err != nil {
		t.Fatal(err)
	}

	stdout := bytes.NewBuffer(nil)
	spec.Process.Args = []string{"ps", "-ef"}
	process, err := ntask.Exec(ctx, t.Name()+"_exec", spec.Process, cio.NewCreator(withStdout(stdout)))
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
	<-processStatusC
	if _, err := process.Delete(ctx); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stdout.String(), "top") {
		t.Errorf("except top process exists in restored container but not, got output %s", stdout.String())
	}

	// we wrote the same thing after attach
	if err := ntask.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-statusC
	ntask.Delete(ctx)
}

func TestCheckpointOnPauseStatus(t *testing.T) {
	if !supportsCriu {
		t.Skip("system does not have criu installed")
	}
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), oci.WithProcessArgs("sleep", "10")))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		task.Resume(ctx)
		task.Delete(ctx)
	}()

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if err := task.Pause(ctx); err != nil {
		t.Fatal(err)
	}

	_, err = container.Checkpoint(ctx, testCheckpointName+"on-pause", []CheckpointOpts{
		WithCheckpointRuntime,
		WithCheckpointRW,
		WithCheckpointTask,
	}...)
	if err != nil {
		t.Fatal(err)
	}

	status, err := task.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if status.Status != Paused {
		t.Fatalf("expected paused state, but got %s", status.Status)
	}

	if err := task.Resume(ctx); err != nil {
		t.Fatal(err)
	}

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	<-statusC
}
