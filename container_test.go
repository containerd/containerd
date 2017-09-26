package containerd

import (
	"bytes"
	"io/ioutil"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	// Register the typeurl
	_ "github.com/containerd/containerd/runtime"

	"github.com/containerd/containerd/errdefs"
	gogotypes "github.com/gogo/protobuf/types"
)

func empty() IOCreation {
	// TODO (@mlaventure) windows searches for pipes
	// when none are provided
	if runtime.GOOS == "windows" {
		return Stdio
	}
	return NullIO
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
	t.Parallel()

	id := t.Name()
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext()
	defer cancel()

	container, err := client.NewContainer(ctx, id, WithNewSpec())
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx)
	if container.ID() != id {
		t.Errorf("expected container id %q but received %q", id, container.ID())
	}
	if _, err = container.Spec(); err != nil {
		t.Error(err)
		return
	}
	if err := container.Delete(ctx); err != nil {
		t.Error(err)
		return
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
	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), withExitStatus(7)), withNewSnapshot(id, image))
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

	if pid := task.Pid(); pid <= 0 {
		t.Errorf("invalid task pid %d", pid)
	}
	if err := task.Start(ctx); err != nil {
		t.Error(err)
		task.Delete(ctx)
		return
	}
	status := <-statusC
	code, _, err := status.Result()
	if err != nil {
		t.Error(err)
		return
	}
	if code != 7 {
		t.Errorf("expected status 7 from wait but received %d", code)
	}

	deleteStatus, err := task.Delete(ctx)
	if err != nil {
		t.Error(err)
		return
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
		ctx, cancel = testContext()
		id          = t.Name()
		expected    = "kingkoye"
	)
	defer cancel()

	if runtime.GOOS != "windows" {
		image, err = client.GetImage(ctx, testImage)
		if err != nil {
			t.Error(err)
			return
		}
	}
	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), withProcessArgs("echo", expected)), withNewSnapshot(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	stdout := bytes.NewBuffer(nil)
	task, err := container.NewTask(ctx, NewIO(bytes.NewBuffer(nil), stdout, bytes.NewBuffer(nil)))
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

	status := <-statusC
	code, _, _ := status.Result()
	if code != 0 {
		t.Errorf("expected status 0 but received %d", code)
	}
	if _, err := task.Delete(ctx); err != nil {
		t.Error(err)
		return
	}

	actual := stdout.String()
	// echo adds a new line
	expected = expected + newLine
	if actual != expected {
		t.Errorf("expected output %q but received %q", expected, actual)
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

	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), withProcessArgs("sleep", "100")), withNewSnapshot(id, image))
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

	finishedC, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
		return
	}

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}
	spec, err := container.Spec()
	if err != nil {
		t.Error(err)
		return
	}

	// start an exec process without running the original container process info
	processSpec := spec.Process
	withExecExitStatus(processSpec, 6)
	execID := t.Name() + "_exec"
	process, err := task.Exec(ctx, execID, processSpec, empty())
	if err != nil {
		t.Error(err)
		return
	}
	processStatusC, err := process.Wait(ctx)
	if err != nil {
		t.Error(err)
		return
	}

	if err := process.Start(ctx); err != nil {
		t.Error(err)
		return
	}

	// wait for the exec to return
	status := <-processStatusC
	code, _, err := status.Result()
	if err != nil {
		t.Error(err)
		return
	}

	if code != 6 {
		t.Errorf("expected exec exit code 6 but received %d", code)
	}
	deleteStatus, err := process.Delete(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	if ec := deleteStatus.ExitCode(); ec != 6 {
		t.Errorf("expected delete exit code 6 but received %d", ec)
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

	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), withProcessArgs("sleep", "100")), withNewSnapshot(id, image))
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

	pid := task.Pid()
	if pid <= 0 {
		t.Errorf("invalid task pid %d", pid)
	}
	processes, err := task.Pids(ctx)
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
	t.Parallel()

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

	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), withCat()), withNewSnapshot(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	const expected = "hello" + newLine
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

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
		return
	}

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
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

	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), withProcessArgs("sleep", "100")), withNewSnapshot(id, image))
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

	err = container.Delete(ctx, WithSnapshotCleanup)
	if err == nil {
		t.Error("delete did not error with running task")
	}
	if !errdefs.IsFailedPrecondition(err) {
		t.Errorf("expected error %q but received %q", errdefs.ErrFailedPrecondition, err)
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
		return
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

	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), withProcessArgs("sleep", "10")), withNewSnapshot(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx)

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
	if !errdefs.IsNotFound(err) {
		t.Errorf("expected error %q but received %q", errdefs.ErrNotFound, err)
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

	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), WithProcessArgs("nothing")), withNewSnapshot(id, image))
	if err != nil {
		t.Error(err)
		return
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

	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), withProcessArgs("sleep", "100")), withNewSnapshot(id, image))
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

	finishedC, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
	}
	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}
	spec, err := container.Spec()
	if err != nil {
		t.Error(err)
		return
	}

	// start an exec process without running the original container process
	processSpec := spec.Process
	processSpec.Args = []string{
		"none",
	}
	execID := t.Name() + "_exec"
	process, err := task.Exec(ctx, execID, processSpec, empty())
	if err != nil {
		t.Error(err)
		return
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

func TestUserNamespaces(t *testing.T) {
	t.Parallel()
	t.Run("WritableRootFS", func(t *testing.T) { testUserNamespaces(t, false) })
	// see #1373 and runc#1572
	t.Run("ReadonlyRootFS", func(t *testing.T) { testUserNamespaces(t, true) })
}

func testUserNamespaces(t *testing.T, readonlyRootFS bool) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext()
		id          = strings.Replace(t.Name(), "/", "-", -1)
	)
	defer cancel()

	if runtime.GOOS != "windows" {
		image, err = client.GetImage(ctx, testImage)
		if err != nil {
			t.Error(err)
			return
		}
	}

	opts := []NewContainerOpts{WithNewSpec(withImageConfig(image),
		withExitStatus(7),
		withUserNamespace(0, 1000, 10000),
	)}
	if readonlyRootFS {
		opts = append(opts, withRemappedSnapshotView(id, image, 1000, 1000))
	} else {
		opts = append(opts, withRemappedSnapshot(id, image, 1000, 1000))
	}

	container, err := client.NewContainer(ctx, id, opts...)
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

	if pid := task.Pid(); pid <= 0 {
		t.Errorf("invalid task pid %d", pid)
	}
	if err := task.Start(ctx); err != nil {
		t.Error(err)
		task.Delete(ctx)
		return
	}
	status := <-statusC
	code, _, err := status.Result()
	if err != nil {
		t.Error(err)
		return
	}
	if code != 7 {
		t.Errorf("expected status 7 from wait but received %d", code)
	}
	deleteStatus, err := task.Delete(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	if ec := deleteStatus.ExitCode(); ec != 7 {
		t.Errorf("expected status 7 from delete but received %d", ec)
	}
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

	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), withExitStatus(7)), withNewSnapshot(id, image))
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

	if pid := task.Pid(); pid <= 0 {
		t.Errorf("invalid task pid %d", pid)
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
		t.Error(err)
		return
	}
	status := <-statusC
	code, _, err := status.Result()
	if err != nil {
		t.Error(err)
		return
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

	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), withProcessArgs("sleep", "100")), withNewSnapshot(id, image))
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

	finishedC, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}
	spec, err := container.Spec()
	if err != nil {
		t.Error(err)
		return
	}

	// start an exec process without running the original container process info
	processSpec := spec.Process
	withExecExitStatus(processSpec, 6)
	execID := t.Name() + "_exec"
	process, err := task.Exec(ctx, execID, processSpec, empty())
	if err != nil {
		t.Error(err)
		return
	}
	defer process.Delete(ctx)

	statusC, err := process.Wait(ctx)
	if err != nil {
		t.Error(err)
		return
	}

	if err := process.Start(ctx); err != nil {
		t.Error(err)
		return
	}

	// wait for the exec to return
	<-statusC

	// try to wait on the process after it has stopped
	statusC, err = process.Wait(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	status := <-statusC
	code, _, err := status.Result()
	if err != nil {
		t.Error(err)
		return
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
	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), withProcessArgs("sleep", "30")), withNewSnapshot(id, image))
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
	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}
	if _, err := task.Delete(ctx); err == nil {
		t.Error("task.Delete of a running task should create an error")
	}
	if _, err := task.Delete(ctx, WithProcessKill); err != nil {
		t.Error(err)
		return
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
	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), withProcessArgs("sleep", "30")), withNewSnapshot(id, image))
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

	// task must be started on windows
	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}
	spec, err := container.Spec()
	if err != nil {
		t.Error(err)
		return
	}

	processSpec := spec.Process
	withExecArgs(processSpec, "sleep", "20")
	execID := t.Name() + "_exec"
	process, err := task.Exec(ctx, execID, processSpec, empty())
	if err != nil {
		t.Error(err)
		return
	}
	if err := process.Start(ctx); err != nil {
		t.Error(err)
		return
	}
	if _, err := process.Delete(ctx); err == nil {
		t.Error("process.Delete should return an error when process is running")
	}
	if _, err := process.Delete(ctx, WithProcessKill); err != nil {
		t.Error(err)
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
		return
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
		ctx, cancel = testContext()
		id          = t.Name()
		expected    = "myhostname"
	)
	defer cancel()

	if runtime.GOOS != "windows" {
		image, err = client.GetImage(ctx, testImage)
		if err != nil {
			t.Error(err)
			return
		}
	}

	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image),
		withProcessArgs("hostname"),
		WithHostname(expected),
	),
		withNewSnapshot(id, image))
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	stdout := bytes.NewBuffer(nil)
	task, err := container.NewTask(ctx, NewIO(bytes.NewBuffer(nil), stdout, bytes.NewBuffer(nil)))
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

	status := <-statusC
	code, _, err := status.Result()
	if err != nil {
		t.Error(err)
		return
	}
	if code != 0 {
		t.Errorf("expected status 0 but received %d", code)
	}
	if _, err := task.Delete(ctx); err != nil {
		t.Error(err)
		return
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

	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), withTrue()), withNewSnapshot(id, image))
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
	}

	startTime := time.Now()
	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}

	status := <-statusC
	code, _, _ := status.Result()
	if code != 0 {
		t.Errorf("expected status 0 but received %d", code)
	}

	if s, err := task.Status(ctx); err != nil {
		t.Errorf("failed to retrieve status: %v", err)
	} else if s.ExitTime.After(startTime) == false {
		t.Errorf("exit time is not after start time: %v <= %v", startTime, s.ExitTime)
	}

	if _, err := task.Delete(ctx); err != nil {
		t.Error(err)
		return
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

	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), withProcessArgs("sleep", "100")), withNewSnapshot(id, image))
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

	finished, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}
	spec, err := container.Spec()
	if err != nil {
		t.Error(err)
		return
	}

	// start an exec process without running the original container process info
	processSpec := spec.Process
	withExecExitStatus(processSpec, 6)
	execID := t.Name() + "_exec"
	process, err := task.Exec(ctx, execID, processSpec, empty())
	if err != nil {
		t.Error(err)
		return
	}
	deleteStatus, err := process.Delete(ctx)
	if err != nil {
		t.Error(err)
		return
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
	container, err := client.NewContainer(ctx, id, WithNewSpec(withImageConfig(image), WithProcessArgs("sleep", "30")), withNewSnapshot(id, image))
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

	metric, err := task.Metrics(ctx)
	if err != nil {
		t.Error(err)
	}
	if metric.ID != id {
		t.Errorf("expected metric id %q but received %q", id, metric.ID)
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
		return
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
	container, err := client.NewContainer(ctx, id,
		WithNewSpec(withImageConfig(image), withExitStatus(0)),
		withNewSnapshot(id, image),
	)
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

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	<-statusC

	if _, err := task.Delete(ctx); err != nil {
		t.Error(err)
		return
	}

	if _, err := task.Metrics(ctx); err == nil {
		t.Errorf("Getting metrics of deleted task should have failed")
	}
}

func TestContainerExtensions(t *testing.T) {
	t.Parallel()

	ctx, cancel := testContext()
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
		cExts := container.Extensions()
		if len(cExts) != 1 {
			t.Fatal("expected 1 container extension")
		}
		if cExts["hello"].TypeUrl != ext.TypeUrl {
			t.Fatalf("got unexpected type url for extension: %s", cExts["hello"].TypeUrl)
		}
		if !bytes.Equal(cExts["hello"].Value, ext.Value) {
			t.Fatalf("expected extension value %q, got: %q", ext.Value, cExts["hello"].Value)
		}
	}

	checkExt(container)

	container, err = client.LoadContainer(ctx, container.ID())
	if err != nil {
		t.Fatal(err)
	}
	checkExt(container)
}
