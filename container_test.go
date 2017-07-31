package containerd

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	// Register the typeurl
	_ "github.com/containerd/containerd/runtime"

	"github.com/containerd/containerd/errdefs"
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

	spec, err := generateSpec()
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

	spec, err := generateSpec(withImageConfig(ctx, image), withExitStatus(7))
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

	spec, err := generateSpec(withImageConfig(ctx, image), withProcessArgs("echo", expected))
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
	expected = expected + newLine
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

	spec, err := generateSpec(withImageConfig(ctx, image), withProcessArgs("sleep", "100"))
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

	finished := make(chan struct{}, 1)
	go func() {
		if _, err := task.Wait(ctx); err != nil {
			t.Error(err)
		}
		close(finished)
	}()

	if err := task.Start(ctx); err != nil {
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
	processStatusC := make(chan uint32, 1)
	go func() {
		status, err := process.Wait(ctx)
		if err != nil {
			t.Error(err)
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

func TestContainerPids(t *testing.T) {
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

	spec, err := generateSpec(withImageConfig(ctx, image), withProcessArgs("sleep", "100"))
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

	if runtime.GOOS == "windows" {
		// On windows we use more and it always adds an extra newline
		// remove it here
		output = strings.TrimSuffix(output, newLine)
	}

	if output != expected {
		t.Errorf("expected output %q but received %q", expected, output)
	}
}

func TestContainerAttach(t *testing.T) {
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

	spec, err := generateSpec(withImageConfig(ctx, image), withProcessArgs("sleep", "100"))
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
	if !errdefs.IsNotFound(err) {
		t.Errorf("expected error %q but received %q", errdefs.ErrNotFound, err)
	}
}

func TestContainerNoBinaryExists(t *testing.T) {
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

	spec, err := generateSpec(withImageConfig(ctx, image), withProcessArgs("nothing"))
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

	task, err := container.NewTask(ctx, Stdio)
	switch runtime.GOOS {
	case "windows":
		if err != nil {
			t.Errorf("failed to create task %v", err)
		}
		if err := task.Start(ctx); err != nil {
			t.Error("task.Start() should return an error when binary does not exist")
			task.Delete(ctx)
		}
	default:
		if err == nil {
			t.Error("NewTask should return an error when binary does not exist")
			task.Delete(ctx)
		}
	}
}

func TestContainerExecNoBinaryExists(t *testing.T) {
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

	spec, err := generateSpec(withImageConfig(ctx, image), withProcessArgs("sleep", "100"))
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

	if err := task.Start(ctx); err != nil {
		t.Error(err)
		return
	}

	finished := make(chan struct{}, 1)
	go func() {
		if _, err := task.Wait(ctx); err != nil {
			t.Error(err)
		}
		close(finished)
	}()

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
	<-finished
}

func TestUserNamespaces(t *testing.T) {
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

	spec, err := generateSpec(
		withImageConfig(ctx, image),
		withExitStatus(7),
		withUserNamespace(0, 1000, 10000),
	)
	if err != nil {
		t.Error(err)
		return
	}
	container, err := client.NewContainer(ctx, id,
		WithSpec(spec),
		withRemappedSnapshot(id, image, 1000, 1000),
	)
	if err != nil {
		t.Error(err)
		return
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

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

func TestWaitStoppedTask(t *testing.T) {
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

	spec, err := generateSpec(withImageConfig(ctx, image), withExitStatus(7))
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
	// wait for the task to stop then call wait again
	<-statusC
	_, err = task.Wait(ctx)
	if err == nil {
		t.Error("Wait after task exits should return an error")
		return
	}
	if !errdefs.IsUnavailable(err) {
		t.Errorf("Wait should return %q when task Stopped: %v", errdefs.ErrUnavailable, err)
	}
}

func TestWaitStoppedProcess(t *testing.T) {
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

	spec, err := generateSpec(withImageConfig(ctx, image), withProcessArgs("sleep", "100"))
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

	finished := make(chan struct{}, 1)
	go func() {
		if _, err := task.Wait(ctx); err != nil {
			t.Error(err)
		}
		close(finished)
	}()

	if err := task.Start(ctx); err != nil {
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
	processStatusC := make(chan uint32, 1)
	go func() {
		status, err := process.Wait(ctx)
		if err != nil {
			t.Error(err)
		}
		processStatusC <- status
	}()

	if err := process.Start(ctx); err != nil {
		t.Error(err)
		return
	}

	// wait for the exec to return
	<-processStatusC
	// try to wait on the process after it has stopped
	_, err = process.Wait(ctx)
	if err == nil {
		t.Error("Wait after process exits should return an error")
		return
	}
	if !errdefs.IsUnavailable(err) {
		t.Errorf("Wait should return %q when process has exited: %v", errdefs.ErrUnavailable, err)
	}
	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
	}
	<-finished
}
