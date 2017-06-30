package containerd

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sync"
	"syscall"
	"testing"

	"github.com/containerd/cgroups"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func empty() IOCreation {
	null := ioutil.Discard
	return NewIO(bytes.NewBuffer(nil), null, null)
}

func TestContainerList(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext()
	defer cancel()

	containers, err := client.Containers(ctx)
	if err != nil {
		t.Errorf("container list returned error %v", err)
		return
	}
	if len(containers) != 0 {
		t.Errorf("expected 0 containers but received %d", len(containers))
	}
}

func TestNewContainer(t *testing.T) {
	id := t.Name()
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	spec, err := GenerateSpec()
	if err != nil {
		t.Error(err)
		return
	}

	ctx, cancel := testContext()
	defer cancel()

	container, err := client.NewContainer(ctx, id, WithSpec(spec))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx)
	if container.ID() != id {
		t.Errorf("expected container id %q but received %q", id, container.ID())
	}
	if spec, err = container.Spec(); err != nil {
		t.Error(err)
		return
	}
	if err := container.Delete(ctx); err != nil {
		t.Error(err)
		return
	}
}

func TestContainerStart(t *testing.T) {
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
	spec, err := GenerateSpec(WithImageConfig(ctx, image), WithProcessArgs("sh", "-c", "exit 7"))
	if err != nil {
		t.Error(err)
		return
	}
	container, err := client.NewContainer(ctx, id, WithSpec(spec), WithNewRootFS(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx, WithRootFSDeletion)

	task, err := container.NewTask(ctx, Stdio)
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

	if pid := task.Pid(); pid <= 0 {
		t.Errorf("invalid task pid %d", pid)
	}
	if err := task.Start(ctx); err != nil {
		t.Error(err)
		task.Delete(ctx)
		return
	}
	status := <-statusC
	if status != 7 {
		t.Errorf("expected status 7 from wait but received %d", status)
	}
	if status, err = task.Delete(ctx); err != nil {
		t.Error(err)
		return
	}
	if status != 7 {
		t.Errorf("expected status 7 from delete but received %d", status)
	}
}

func TestContainerOutput(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		ctx, cancel = testContext()
		id          = t.Name()
		expected    = "kingkoye"
	)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Error(err)
		return
	}
	spec, err := GenerateSpec(WithImageConfig(ctx, image), WithProcessArgs("echo", expected))
	if err != nil {
		t.Error(err)
		return
	}
	container, err := client.NewContainer(ctx, id, WithSpec(spec), WithNewRootFS(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx, WithRootFSDeletion)

	stdout := bytes.NewBuffer(nil)
	task, err := container.NewTask(ctx, NewIO(bytes.NewBuffer(nil), stdout, bytes.NewBuffer(nil)))
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

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}

	status := <-statusC
	if status != 0 {
		t.Errorf("expected status 0 but received %d", status)
	}
	if _, err := task.Delete(ctx); err != nil {
		t.Error(err)
		return
	}

	actual := stdout.String()
	// echo adds a new line
	expected = expected + "\n"
	if actual != expected {
		t.Errorf("expected output %q but received %q", expected, actual)
	}
}

func TestContainerExec(t *testing.T) {
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
	spec, err := GenerateSpec(WithImageConfig(ctx, image), WithProcessArgs("sleep", "100"))
	if err != nil {
		t.Error(err)
		return
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

	finished := make(chan struct{}, 1)
	go func() {
		if _, err := task.Wait(ctx); err != nil {
			t.Error(err)
		}
		close(finished)
	}()

	// start an exec process without running the original container process info
	processSpec := spec.Process
	processSpec.Args = []string{
		"sh", "-c",
		"exit 6",
	}

	process, err := task.Exec(ctx, processSpec, empty())
	if err != nil {
		t.Error(err)
		return
	}
	processStatusC := make(chan uint32, 1)
	go func() {
		status, err := process.Wait(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		processStatusC <- status
	}()

	if err := process.Start(ctx); err != nil {
		t.Error(err)
		return
	}

	// wait for the exec to return
	status := <-processStatusC

	if status != 6 {
		t.Errorf("expected exec exit code 6 but received %d", status)
	}
	deleteStatus, err := process.Delete(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	if deleteStatus != 6 {
		t.Errorf("expected delete exit code e6 but received %d", deleteStatus)
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
	}
	<-finished
}

func TestContainerProcesses(t *testing.T) {
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
	spec, err := GenerateSpec(WithImageConfig(ctx, image), WithProcessArgs("sleep", "100"))
	if err != nil {
		t.Error(err)
		return
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

	pid := task.Pid()
	if pid <= 0 {
		t.Errorf("invalid task pid %d", pid)
	}
	processes, err := task.Processes(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	if l := len(processes); l != 1 {
		t.Errorf("expected 1 process but received %d", l)
	}
	if len(processes) > 0 {
		actual := processes[0]
		if pid != actual {
			t.Errorf("expected pid %d but received %d", pid, actual)
		}
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
	}
	<-statusC
}

func TestContainerCloseIO(t *testing.T) {
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
	spec, err := GenerateSpec(WithImageConfig(ctx, image), WithProcessArgs("cat"))
	if err != nil {
		t.Error(err)
		return
	}
	container, err := client.NewContainer(ctx, id, WithSpec(spec), WithNewRootFS(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx, WithRootFSDeletion)

	const expected = "hello\n"
	stdout := bytes.NewBuffer(nil)

	r, w, err := os.Pipe()
	if err != nil {
		t.Error(err)
		return
	}

	task, err := container.NewTask(ctx, NewIO(r, stdout, ioutil.Discard))
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

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}

	if _, err := fmt.Fprint(w, expected); err != nil {
		t.Error(err)
	}
	w.Close()
	if err := task.CloseIO(ctx, WithStdinCloser); err != nil {
		t.Error(err)
	}

	<-statusC

	if _, err := task.Delete(ctx); err != nil {
		t.Error(err)
	}

	output := stdout.String()

	if output != expected {
		t.Errorf("expected output %q but received %q", expected, output)
	}
}

func TestContainerAttach(t *testing.T) {
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
	spec, err := GenerateSpec(WithImageConfig(ctx, image), WithProcessArgs("cat"))
	if err != nil {
		t.Error(err)
		return
	}
	container, err := client.NewContainer(ctx, id, WithSpec(spec), WithNewRootFS(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx, WithRootFSDeletion)

	expected := "hello\n"
	stdout := bytes.NewBuffer(nil)

	r, w, err := os.Pipe()
	if err != nil {
		t.Error(err)
		return
	}
	or, ow, err := os.Pipe()
	if err != nil {
		t.Error(err)
		return
	}

	wg := &sync.WaitGroup{}

	wg.Add(1)
	go func() {
		io.Copy(stdout, or)
		wg.Done()
	}()

	task, err := container.NewTask(ctx, NewIO(r, ow, ioutil.Discard))
	if err != nil {
		t.Error(err)
		return
	}
	defer task.Delete(ctx)
	originalIO := task.IO()

	statusC := make(chan uint32, 1)
	go func() {
		status, err := task.Wait(ctx)
		if err != nil {
			t.Error(err)
		}
		statusC <- status
	}()

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}

	if _, err := fmt.Fprint(w, expected); err != nil {
		t.Error(err)
	}
	w.Close()

	// load the container and re-load the task
	if container, err = client.LoadContainer(ctx, id); err != nil {
		t.Error(err)
		return
	}

	// create new IO for the loaded task
	if r, w, err = os.Pipe(); err != nil {
		t.Error(err)
		return
	}
	if task, err = container.Task(ctx, WithAttach(r, ow, ioutil.Discard)); err != nil {
		t.Error(err)
		return
	}

	if _, err := fmt.Fprint(w, expected); err != nil {
		t.Error(err)
	}
	w.Close()

	if err := task.CloseIO(ctx, WithStdinCloser); err != nil {
		t.Error(err)
	}

	<-statusC

	originalIO.Close()
	if _, err := task.Delete(ctx); err != nil {
		t.Error(err)
	}
	ow.Close()

	wg.Wait()
	output := stdout.String()

	// we wrote the same thing after attach
	expected = expected + expected
	if output != expected {
		t.Errorf("expected output %q but received %q", expected, output)
	}
}

func TestDeleteRunningContainer(t *testing.T) {
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
	spec, err := GenerateSpec(WithImageConfig(ctx, image), WithProcessArgs("sleep", "100"))
	if err != nil {
		t.Error(err)
		return
	}
	container, err := client.NewContainer(ctx, id, WithSpec(spec), WithImage(image), WithNewRootFS(id, image))
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

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}

	err = container.Delete(ctx, WithRootFSDeletion)
	if err == nil {
		t.Error("delete did not error with running task")
	}
	if err != ErrDeleteRunningTask {
		t.Errorf("expected error %q but received %q", ErrDeleteRunningTask, err)
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
		return
	}
	<-statusC
}

func TestContainerKill(t *testing.T) {
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
	spec, err := GenerateSpec(WithImageConfig(ctx, image), WithProcessArgs("sh", "-c", "cat"))
	if err != nil {
		t.Error(err)
		return
	}
	container, err := client.NewContainer(ctx, id, WithSpec(spec), WithImage(image), WithNewRootFS(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx)

	task, err := container.NewTask(ctx, Stdio)
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

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
		return
	}
	<-statusC

	err = task.Kill(ctx, syscall.SIGTERM)
	if err == nil {
		t.Error("second call to kill should return an error")
		return
	}
	if err != ErrProcessExited {
		t.Errorf("expected error %q but received %q", ErrProcessExited, err)
	}
}

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
	spec, err := GenerateSpec(WithImageConfig(ctx, image), WithProcessArgs("sleep", "30"))
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
