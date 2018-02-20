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

package containerd

import (
	"syscall"
	"testing"

	"github.com/containerd/containerd/oci"
)

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
		ctx, cancel = testContext()
		id          = t.Name()
	)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id, WithNewSpec(oci.WithImageConfig(image), oci.WithProcessArgs("sleep", "100")), WithNewSnapshot(id, image))
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

	checkpoint, err := task.Checkpoint(ctx, WithExit)
	if err != nil {
		t.Fatal(err)
	}

	<-statusC

	if _, err := task.Delete(ctx); err != nil {
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
	ctx, cancel := testContext()
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id, WithNewSpec(oci.WithImageConfig(image), oci.WithProcessArgs("sleep", "100")), WithNewSnapshot(id, image))
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

	checkpoint, err := task.Checkpoint(ctx, WithExit)
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
	if container, err = client.NewContainer(ctx, id, WithCheckpoint(checkpoint, id)); err != nil {
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
	client, err := New(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		ctx, cancel = testContext()
		id          = t.Name()
	)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id, WithNewSpec(oci.WithImageConfig(image), oci.WithProcessArgs("sleep", "100")), WithNewSnapshot(id, image))
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

	if _, err := task.Checkpoint(ctx); err != nil {
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
