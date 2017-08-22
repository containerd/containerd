// +build linux

package containerd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/cgroups"
	"github.com/containerd/containerd/linux/runcopts"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

func TestContainerUpdate(t *testing.T) {
	t.Parallel()

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
		t.Error(err)
		return
	}
	spec, err := generateSpec(WithImageConfig(ctx, image), withProcessArgs("sleep", "30"))
	if err != nil {
		t.Error(err)
		return
	}
	limit := int64(32 * 1024 * 1024)
	spec.Linux.Resources.Memory = &specs.LinuxMemory{
		Limit: &limit,
	}
	container, err := client.NewContainer(ctx, id, WithSpec(spec), WithNewSnapshot(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Error(err)
		return
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
		return
	}

	// check that the task has a limit of 32mb
	cgroup, err := cgroups.Load(cgroups.V1, cgroups.PidPath(int(task.Pid())))
	if err != nil {
		t.Error(err)
		return
	}
	stat, err := cgroup.Stat(cgroups.IgnoreNotExist)
	if err != nil {
		t.Error(err)
		return
	}
	if int64(stat.Memory.Usage.Limit) != limit {
		t.Errorf("expected memory limit to be set to %d but received %d", limit, stat.Memory.Usage.Limit)
		return
	}
	limit = 64 * 1024 * 1024
	if err := task.Update(ctx, WithResources(&specs.LinuxResources{
		Memory: &specs.LinuxMemory{
			Limit: &limit,
		},
	})); err != nil {
		t.Error(err)
	}
	// check that the task has a limit of 64mb
	if stat, err = cgroup.Stat(cgroups.IgnoreNotExist); err != nil {
		t.Error(err)
		return
	}
	if int64(stat.Memory.Usage.Limit) != limit {
		t.Errorf("expected memory limit to be set to %d but received %d", limit, stat.Memory.Usage.Limit)
	}
	if err := task.Kill(ctx, unix.SIGKILL); err != nil {
		t.Error(err)
		return
	}

	<-statusC
}

func TestShimInCgroup(t *testing.T) {
	t.Parallel()

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
		t.Error(err)
		return
	}
	spec, err := GenerateSpec(WithImageConfig(ctx, image), WithProcessArgs("sleep", "30"))
	if err != nil {
		t.Error(err)
		return
	}
	container, err := client.NewContainer(ctx, id, WithSpec(spec), WithNewSnapshot(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx, WithSnapshotCleanup)
	// create a cgroup for the shim to use
	path := "/containerd/shim"
	cg, err := cgroups.New(cgroups.V1, cgroups.StaticPath(path), &specs.LinuxResources{})
	if err != nil {
		t.Error(err)
		return
	}
	defer cg.Delete()

	task, err := container.NewTask(ctx, empty(), func(_ context.Context, client *Client, r *TaskInfo) error {
		r.Options = &runcopts.CreateOptions{
			ShimCgroup: path,
		}
		return nil
	})
	if err != nil {
		t.Error(err)
		return
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
		return
	}

	// check to see if the shim is inside the cgroup
	processes, err := cg.Processes(cgroups.Devices, false)
	if err != nil {
		t.Error(err)
		return
	}
	if len(processes) == 0 {
		t.Errorf("created cgroup should have atleast one process inside: %d", len(processes))
	}
	if err := task.Kill(ctx, unix.SIGKILL); err != nil {
		t.Error(err)
		return
	}

	<-statusC
}

func TestDaemonRestart(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext()
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Error(err)
		return
	}

	spec, err := generateSpec(withImageConfig(ctx, image), withProcessArgs("sleep", "30"))
	if err != nil {
		t.Error(err)
		return
	}
	container, err := client.NewContainer(ctx, id, WithSpec(spec), withNewSnapshot(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Error(err)
		return
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
		return
	}

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}

	if err := ctrd.Restart(); err != nil {
		t.Fatal(err)
	}

	status := <-statusC
	_, _, err = status.Result()
	if err == nil {
		t.Errorf(`first task.Wait() should have failed with "transport is closing"`)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 2*time.Second)
	serving, err := client.IsServing(waitCtx)
	waitCancel()
	if !serving {
		t.Fatalf("containerd did not start within 2s: %v", err)
	}

	statusC, err = task.Wait(ctx)
	if err != nil {
		t.Error(err)
		return
	}

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	<-statusC
}

func TestContainerAttach(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		// On windows, closing the write side of the pipe closes the read
		// side, sending an EOF to it and preventing reopening it.
		// Hence this test will always fails on windows
		t.Skip("invalid logic on windows")
	}

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext()
		id          = t.Name()
	)
	defer cancel()

	if runtime.GOOS != "windows" {
		image, err = client.GetImage(ctx, testImage)
		if err != nil {
			t.Error(err)
			return
		}
	}

	spec, err := generateSpec(withImageConfig(ctx, image), withCat())
	if err != nil {
		t.Error(err)
		return
	}
	container, err := client.NewContainer(ctx, id, WithSpec(spec), withNewSnapshot(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	expected := "hello" + newLine

	direct, err := NewDirectIO(ctx, false)
	if err != nil {
		t.Error(err)
		return
	}
	defer direct.Delete()
	var (
		wg  sync.WaitGroup
		buf = bytes.NewBuffer(nil)
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(buf, direct.Stdout)
	}()

	task, err := container.NewTask(ctx, direct.IOCreate)
	if err != nil {
		t.Error(err)
		return
	}
	defer task.Delete(ctx)

	status, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}

	if _, err := fmt.Fprint(direct.Stdin, expected); err != nil {
		t.Error(err)
	}

	// load the container and re-load the task
	if container, err = client.LoadContainer(ctx, id); err != nil {
		t.Error(err)
		return
	}

	if task, err = container.Task(ctx, direct.IOAttach); err != nil {
		t.Error(err)
		return
	}

	if _, err := fmt.Fprint(direct.Stdin, expected); err != nil {
		t.Error(err)
	}

	direct.Stdin.Close()

	if err := task.CloseIO(ctx, WithStdinCloser); err != nil {
		t.Error(err)
	}

	<-status

	wg.Wait()
	if _, err := task.Delete(ctx); err != nil {
		t.Error(err)
	}

	output := buf.String()

	// we wrote the same thing after attach
	expected = expected + expected
	if output != expected {
		t.Errorf("expected output %q but received %q", expected, output)
	}
}
