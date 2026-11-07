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

package integration

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/cgroups/v3"
	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2"
	"github.com/containerd/errdefs"
	"github.com/containerd/fifo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
)

// TestConcurrentTaskCreatePreservesSuccessfulTask checks that two racing
// Creates for the same container leave one manageable task.
func TestConcurrentTaskCreatePreservesSuccessfulTask(t *testing.T) {
	if cgroups.Mode() != cgroups.Unified {
		t.Skip("requires cgroup v2")
	}

	const (
		ns = "concurrent-task-create"
		id = "concurrent-task-create"
	)

	shimCgroup := "/containerd-" + id
	createShimCgroup(t, shimCgroup)

	workDir := t.TempDir()
	gatePath := filepath.Join(workDir, "bootstrap-gate.fifo")
	require.NoError(t, unix.Mkfifo(gatePath, 0600))

	require.NoError(t, os.WriteFile(filepath.Join(workDir, "config.toml"), []byte("version = 3\n"), 0o600))

	ctrd := newCtrdProc(t, *containerdBin, workDir, nil)
	require.NoError(t, ctrd.isReady())

	client, err := containerd.New(ctrd.grpcAddress())
	require.NoError(t, err)

	t.Cleanup(func() {
		if t.Failed() {
			dumpFileContent(t, ctrd.logPath())
		}
		assert.NoError(t, client.Close())
		assert.NoError(t, ctrd.kill(syscall.SIGTERM))
		assert.NoError(t, ctrd.wait(5*time.Minute))
	})

	ctx, cancel := context.WithTimeout(namespaces.WithNamespace(context.Background(), ns), timeout)
	defer cancel()

	image, err := client.Pull(ctx, images.Get(images.BusyBox), containerd.WithPullUnpack)
	require.NoError(t, err)

	container, err := client.NewContainer(ctx, id,
		containerd.WithNewSnapshot(id, image),
		containerd.WithNewSpec(oci.WithImageConfig(image), oci.WithProcessArgs("sleep", "300"),
			oci.WithAnnotations(map[string]string{
				// Hold the shim's address until the duplicate Create and
				// registration checks finish.
				"io.containerd.runtime.v2.shim.failpoint.AfterStartShim": "1*pause_on_fifo(" + gatePath + ")",
			})),
		containerd.WithRuntime("io.containerd.runc-fp.v1", nil),
	)
	require.NoError(t, err)
	defer container.Delete(context.WithoutCancel(ctx), containerd.WithSnapshotCleanup)

	type createResult struct {
		task containerd.Task
		err  error
	}

	gateCtx, cancelGate := context.WithTimeout(ctx, 30*time.Second)
	defer cancelGate()

	firstCreateCh := make(chan createResult, 1)
	go func() {
		defer close(firstCreateCh)
		defer cancelGate()
		task, err := container.NewTask(ctx, cio.NullIO, containerd.WithShimCgroup(shimCgroup))
		firstCreateCh <- createResult{task: task, err: err}
	}()

	defer func() {
		// Stop the first Create if it is still running, then delete
		// any task the test left behind.
		cancel()
		<-firstCreateCh

		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		if task, err := container.Task(cctx, nil); err == nil {
			if _, err := task.Delete(cctx, containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
				t.Errorf("failed to delete task: %v", err)
			}
		}
	}()

	// The writer connects only after bootstrap reaches pause_on_fifo. Keep
	// the shim unregistered until both checks finish by withholding the byte.
	gate, err := fifo.OpenFifo(gateCtx, gatePath, os.O_WRONLY, 0)
	if err != nil {
		select {
		case res := <-firstCreateCh:
			t.Fatalf("first Create returned before bootstrap paused: %v", res.err)
		default:
			t.Fatalf("wait for bootstrap to pause: %v", err)
		}
	}
	defer gate.Close()

	_, err = container.NewTask(ctx, cio.NullIO)
	require.Truef(t, errdefs.IsAlreadyExists(err), "concurrent Create must fail with AlreadyExists, got %v", err)
	require.ErrorContains(t, err, "bundle for "+id, "concurrent Create must be rejected by NewBundle")

	_, err = container.Task(ctx, nil)
	require.Truef(t, errdefs.IsNotFound(err), "first task must not be registered yet, got %v", err)

	t.Log("unpause first shim")
	_, err = gate.Write([]byte{1})
	require.NoError(t, err, "release the first shim")
	res := <-firstCreateCh
	require.NoError(t, res.err, "first Create must succeed")
	require.NoError(t, os.Remove(gatePath))

	task := res.task
	require.NoError(t, task.Start(ctx), "first task must be startable")

	status, err := task.Status(ctx)
	require.NoError(t, err)

	require.Equal(t, containerd.Running, status.Status)
	_, err = task.Delete(ctx, containerd.WithProcessKill)
	require.NoError(t, err, "first task must be deletable")

	spec, err := container.Spec(ctx)
	require.NoError(t, err)
	delete(spec.Annotations, "io.containerd.runtime.v2.shim.failpoint.AfterStartShim")
	require.NoError(t, container.Update(ctx, containerd.UpdateContainerOpts(containerd.WithSpec(spec))))

	task, err = container.NewTask(ctx, cio.NullIO)
	require.NoError(t, err, "container must accept a new task")

	_, err = task.Delete(ctx, containerd.WithProcessKill)
	require.NoError(t, err)
}

// createShimCgroup creates a cgroup v2 group and cleans up any remaining shims
// when the test ends, including shims that containerd has not registered.
func createShimCgroup(t *testing.T, path string) {
	t.Helper()

	cg, err := cgroupsv2.NewManager("/sys/fs/cgroup", path, &cgroupsv2.Resources{})
	require.NoError(t, err)

	t.Cleanup(func() {
		assert.NoError(t, cg.Kill())

		assert.Eventually(t, func() bool { return cg.Delete() == nil },
			30*time.Second, 100*time.Millisecond, "failed to delete cgroup %s", path)
	})
}
