// +build linux

package containerd

import (
	"syscall"
	"testing"

	"github.com/containerd/cgroups"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestContainerUpdate(t *testing.T) {
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
	container, err := client.NewContainer(ctx, id, WithSpec(spec), WithNewRootFS(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx, WithRootFSDeletion)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Error(err)
		return
	}
	defer task.Delete(ctx)

	statusC := make(chan uint32, 1)
	go func() {
		status, err := task.Wait(ctx)
		if err != nil {
			t.Error(err)
		}
		statusC <- status
	}()

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
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
		return
	}

	<-statusC
}
