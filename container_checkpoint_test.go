// +build linux

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
	"fmt"
	"io"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/runtime/linux/runctypes"
	"github.com/containerd/containerd/runtime/v2/runc/options"
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
		ctx, cancel = testContext()
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
			oci.WithTTY))
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

	checkpoint, err := task.Checkpoint(ctx, withExit(client))
	if err != nil {
		t.Fatal(err)
	}

	<-statusC

	if _, err := task.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	direct.Delete()
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
		ctx, cancel = testContext()
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

	checkpoint, err := task.Checkpoint(ctx, withExit(client))
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

	checkpoint, err := task.Checkpoint(ctx, withExit(client))
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

func withExit(client *Client) CheckpointTaskOpts {
	return func(r *CheckpointTaskInfo) error {
		switch client.runtime {
		case "io.containerd.runc.v1":
			r.Options = &options.CheckpointOptions{
				Exit: true,
			}
		default:
			r.Options = &runctypes.CheckpointOptions{
				Exit: true,
			}
		}
		return nil
	}
}
