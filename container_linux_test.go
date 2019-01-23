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
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/cgroups"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/runtime/linux/runctypes"
	"github.com/containerd/containerd/runtime/v2/runc/options"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

func TestTaskUpdate(t *testing.T) {
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
		t.Fatal(err)
	}
	limit := int64(32 * 1024 * 1024)
	memory := func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
		s.Linux.Resources.Memory = &specs.LinuxMemory{
			Limit: &limit,
		}
		return nil
	}
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), withProcessArgs("sleep", "30"), memory))
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

	// check that the task has a limit of 32mb
	cgroup, err := cgroups.Load(cgroups.V1, cgroups.PidPath(int(task.Pid())))
	if err != nil {
		t.Fatal(err)
	}
	stat, err := cgroup.Stat(cgroups.IgnoreNotExist)
	if err != nil {
		t.Fatal(err)
	}
	if int64(stat.Memory.Usage.Limit) != limit {
		t.Fatalf("expected memory limit to be set to %d but received %d", limit, stat.Memory.Usage.Limit)
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
		t.Fatal(err)
	}
	if int64(stat.Memory.Usage.Limit) != limit {
		t.Errorf("expected memory limit to be set to %d but received %d", limit, stat.Memory.Usage.Limit)
	}
	if err := task.Kill(ctx, unix.SIGKILL); err != nil {
		t.Fatal(err)
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
	if client.runtime == "io.containerd.runc.v1" {
		t.Skip()
	}

	var (
		ctx, cancel = testContext()
		id          = t.Name()
	)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), oci.WithProcessArgs("sleep", "30")))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)
	// create a cgroup for the shim to use
	path := "/containerd/shim"
	cg, err := cgroups.New(cgroups.V1, cgroups.StaticPath(path), &specs.LinuxResources{})
	if err != nil {
		t.Fatal(err)
	}
	defer cg.Delete()

	task, err := container.NewTask(ctx, empty(), func(_ context.Context, client *Client, r *TaskInfo) error {
		r.Options = &runctypes.CreateOptions{
			ShimCgroup: path,
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// check to see if the shim is inside the cgroup
	processes, err := cg.Processes(cgroups.Devices, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) == 0 {
		t.Errorf("created cgroup should have atleast one process inside: %d", len(processes))
	}
	if err := task.Kill(ctx, unix.SIGKILL); err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), withProcessArgs("sleep", "30")))
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

	var exitStatus ExitStatus
	if err := ctrd.Restart(func() {
		exitStatus = <-statusC
	}); err != nil {
		t.Fatal(err)
	}

	if exitStatus.Error() == nil {
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
		t.Fatal(err)
	}

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	<-statusC
}

func TestShimDoesNotLeakPipes(t *testing.T) {
	containerdPid := ctrd.cmd.Process.Pid
	initialPipes, err := numPipes(containerdPid)
	if err != nil {
		t.Fatal(err)
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

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), withProcessArgs("sleep", "30")))
	if err != nil {
		t.Fatal(err)
	}

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}

	exitChannel, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	<-exitChannel

	if _, err := task.Delete(ctx); err != nil {
		t.Fatal(err)
	}

	if err := container.Delete(ctx, WithSnapshotCleanup); err != nil {
		t.Fatal(err)
	}

	currentPipes, err := numPipes(containerdPid)
	if err != nil {
		t.Fatal(err)
	}

	if initialPipes != currentPipes {
		t.Errorf("Pipes have leaked after container has been deleted. Initially there were %d pipes, after container deletion there were %d pipes", initialPipes, currentPipes)
	}
}

func numPipes(pid int) (int, error) {
	cmd := exec.Command("sh", "-c", fmt.Sprintf("lsof -p %d | grep pipe", pid))

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	return strings.Count(stdout.String(), "\n"), nil
}

func TestDaemonReconnectsToShimIOPipesOnRestart(t *testing.T) {
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
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), withProcessArgs("sleep", "30")))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	_, err = task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if err := ctrd.Restart(nil); err != nil {
		t.Fatal(err)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 2*time.Second)
	serving, err := client.IsServing(waitCtx)
	waitCancel()
	if !serving {
		t.Fatalf("containerd did not start within 2s: %v", err)
	}

	// After we restared containerd we write some messages to the log pipes, simulating shim writing stuff there.
	// Then we make sure that these messages are available on the containerd log thus proving that the server reconnected to the log pipes
	runtimeVersion := getRuntimeVersion()
	logDirPath := getLogDirPath(runtimeVersion, id)

	switch runtimeVersion {
	case "v1":
		writeToFile(t, filepath.Join(logDirPath, "shim.stdout.log"), fmt.Sprintf("%s writing to stdout\n", id))
		writeToFile(t, filepath.Join(logDirPath, "shim.stderr.log"), fmt.Sprintf("%s writing to stderr\n", id))
	case "v2":
		writeToFile(t, filepath.Join(logDirPath, "log"), fmt.Sprintf("%s writing to log\n", id))
	}

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	<-statusC

	stdioContents, err := ioutil.ReadFile(ctrdStdioFilePath)
	if err != nil {
		t.Fatal(err)
	}

	switch runtimeVersion {
	case "v1":
		if !strings.Contains(string(stdioContents), fmt.Sprintf("%s writing to stdout", id)) {
			t.Fatal("containerd did not connect to the shim stdout pipe")
		}
		if !strings.Contains(string(stdioContents), fmt.Sprintf("%s writing to stderr", id)) {
			t.Fatal("containerd did not connect to the shim stderr pipe")
		}
	case "v2":
		if !strings.Contains(string(stdioContents), fmt.Sprintf("%s writing to log", id)) {
			t.Fatal("containerd did not connect to the shim log pipe")
		}
	}
}

func writeToFile(t *testing.T, filePath, message string) {
	writer, err := os.OpenFile(filePath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(message); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func getLogDirPath(runtimeVersion, id string) string {
	switch runtimeVersion {
	case "v1":
		return filepath.Join(defaultRoot, "io.containerd.runtime.v1.linux", testNamespace, id)
	case "v2":
		return filepath.Join(defaultState, "io.containerd.runtime.v2.task", testNamespace, id)
	default:
		panic(fmt.Errorf("Unsupported runtime version %s", runtimeVersion))
	}
}

func getRuntimeVersion() string {
	switch rt := os.Getenv("TEST_RUNTIME"); rt {
	case "io.containerd.runc.v1":
		return "v2"
	default:
		return "v1"
	}
}

func TestContainerPTY(t *testing.T) {
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

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), oci.WithTTY, withProcessArgs("echo", "hello")))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	direct, err := newDirectIO(ctx, true)
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	status, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	<-status
	wg.Wait()

	if err := direct.Close(); err != nil {
		t.Error(err)
	}

	out := buf.String()
	if !strings.ContainsAny(fmt.Sprintf("%#q", out), `\x00`) {
		t.Fatal(`expected \x00 in output`)
	}
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

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), withCat()))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	expected := "hello" + newLine

	direct, err := newDirectIO(ctx, false)
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	status, err := task.Wait(ctx)
	if err != nil {
		t.Error(err)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := fmt.Fprint(direct.Stdin, expected); err != nil {
		t.Error(err)
	}

	// load the container and re-load the task
	if container, err = client.LoadContainer(ctx, id); err != nil {
		t.Fatal(err)
	}

	if task, err = container.Task(ctx, direct.IOAttach); err != nil {
		t.Fatal(err)
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

func newDirectIO(ctx context.Context, terminal bool) (*directIO, error) {
	fifos, err := cio.NewFIFOSetInDir("", "", terminal)
	if err != nil {
		return nil, err
	}
	dio, err := cio.NewDirectIO(ctx, fifos)
	if err != nil {
		return nil, err
	}
	return &directIO{DirectIO: *dio}, nil
}

type directIO struct {
	cio.DirectIO
}

// ioCreate returns IO available for use with task creation
func (f *directIO) IOCreate(id string) (cio.IO, error) {
	return f, nil
}

// ioAttach returns IO available for use with task attachment
func (f *directIO) IOAttach(set *cio.FIFOSet) (cio.IO, error) {
	return f, nil
}

func (f *directIO) Cancel() {
	// nothing to cancel as all operations are handled externally
}

// Close closes all open fds
func (f *directIO) Close() error {
	err := f.Stdin.Close()
	if err2 := f.Stdout.Close(); err == nil {
		err = err2
	}
	if err2 := f.Stderr.Close(); err == nil {
		err = err2
	}
	return err
}

// Delete removes the underlying directory containing fifos
func (f *directIO) Delete() error {
	return f.DirectIO.Close()
}

func TestContainerUsername(t *testing.T) {
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

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := newDirectIO(ctx, false)
	if err != nil {
		t.Fatal(err)
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

	// squid user in the alpine image has a uid of 31
	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), oci.WithUsername("squid"), oci.WithProcessArgs("id", "-u")),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

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
	<-statusC

	wg.Wait()

	output := strings.TrimSuffix(buf.String(), "\n")
	if output != "31" {
		t.Errorf("expected squid uid to be 31 but received %q", output)
	}
}

func TestContainerUser(t *testing.T) {
	t.Parallel()
	t.Run("UserNameAndGroupName", func(t *testing.T) { testContainerUser(t, "squid:squid", "31:31") })
	t.Run("UserIDAndGroupName", func(t *testing.T) { testContainerUser(t, "1001:squid", "1001:31") })
	t.Run("UserNameAndGroupID", func(t *testing.T) { testContainerUser(t, "squid:1002", "31:1002") })
	t.Run("UserIDAndGroupID", func(t *testing.T) { testContainerUser(t, "1001:1002", "1001:1002") })
}

func testContainerUser(t *testing.T, userstr, expectedOutput string) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext()
		id          = strings.Replace(t.Name(), "/", "_", -1)
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := newDirectIO(ctx, false)
	if err != nil {
		t.Fatal(err)
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

	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), oci.WithUser(userstr), oci.WithProcessArgs("sh", "-c", "echo $(id -u):$(id -g)")),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

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
	<-statusC

	wg.Wait()

	output := strings.TrimSuffix(buf.String(), "\n")
	if output != expectedOutput {
		t.Errorf("expected uid:gid to be %q, but received %q", expectedOutput, output)
	}
}

func TestContainerAttachProcess(t *testing.T) {
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

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), withProcessArgs("sleep", "100")))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	expected := "hello" + newLine

	// creating IO early for easy resource cleanup
	direct, err := newDirectIO(ctx, false)
	if err != nil {
		t.Fatal(err)
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

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	status, err := task.Wait(ctx)
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

	processSpec := spec.Process
	processSpec.Args = []string{"cat"}
	execID := t.Name() + "_exec"
	process, err := task.Exec(ctx, execID, processSpec, direct.IOCreate)
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

	if _, err := fmt.Fprint(direct.Stdin, expected); err != nil {
		t.Error(err)
	}

	if process, err = task.LoadProcess(ctx, execID, direct.IOAttach); err != nil {
		t.Fatal(err)
	}

	if _, err := fmt.Fprint(direct.Stdin, expected); err != nil {
		t.Error(err)
	}

	direct.Stdin.Close()

	if err := process.CloseIO(ctx, WithStdinCloser); err != nil {
		t.Error(err)
	}

	<-processStatusC

	wg.Wait()

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
	}

	output := buf.String()

	// we wrote the same thing after attach
	expected = expected + expected
	if output != expected {
		t.Errorf("expected output %q but received %q", expected, output)
	}
	<-status
}

func TestContainerUserID(t *testing.T) {
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

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := newDirectIO(ctx, false)
	if err != nil {
		t.Fatal(err)
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

	// adm user in the alpine image has a uid of 3 and gid of 4.
	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), oci.WithUserID(3), oci.WithProcessArgs("sh", "-c", "echo $(id -u):$(id -g)")),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

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
	<-statusC

	wg.Wait()

	output := strings.TrimSuffix(buf.String(), "\n")
	if output != "3:4" {
		t.Errorf("expected uid:gid to be 3:4, but received %q", output)
	}
}

func TestContainerKillAll(t *testing.T) {
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

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image),
			withProcessArgs("sh", "-c", "top"),
			oci.WithHostNamespace(specs.PIDNamespace),
		),
	)
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

	if err := task.Kill(ctx, syscall.SIGKILL, WithKillAll); err != nil {
		t.Error(err)
	}

	<-statusC
	if _, err := task.Delete(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonRestartWithRunningShim(t *testing.T) {
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
		t.Error(err)
	}

	pid := task.Pid()
	if pid < 1 {
		t.Fatalf("invalid task pid %d", pid)
	}

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	var exitStatus ExitStatus
	if err := ctrd.Restart(func() {
		exitStatus = <-statusC
	}); err != nil {
		t.Fatal(err)
	}

	if exitStatus.Error() == nil {
		t.Errorf(`first task.Wait() should have failed with "transport is closing"`)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	c, err := ctrd.waitForStart(waitCtx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	statusC, err = task.Wait(ctx)
	if err != nil {
		t.Error(err)
	}

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	<-statusC

	if err := unix.Kill(int(pid), 0); err != unix.ESRCH {
		t.Errorf("pid %d still exists", pid)
	}
}

func TestContainerRuntimeOptionsv1(t *testing.T) {
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

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(
		ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), withExitStatus(7)),
		WithRuntime("io.containerd.runtime.v1.linux", &runctypes.RuncOptions{Runtime: "no-runc"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err == nil {
		t.Errorf("task creation should have failed")
		task.Delete(ctx)
		return
	}
	if !strings.Contains(err.Error(), `"no-runc"`) {
		t.Errorf("task creation should have failed because of lack of executable. Instead failed with: %v", err.Error())
	}
}

func TestContainerRuntimeOptionsv2(t *testing.T) {
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

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(
		ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), withExitStatus(7)),
		WithRuntime("io.containerd.runc.v1", &options.Options{BinaryName: "no-runc"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err == nil {
		t.Errorf("task creation should have failed")
		task.Delete(ctx)
		return
	}
	if !strings.Contains(err.Error(), `"no-runc"`) {
		t.Errorf("task creation should have failed because of lack of executable. Instead failed with: %v", err.Error())
	}
}

func initContainerAndCheckChildrenDieOnKill(t *testing.T, opts ...oci.SpecOpts) {
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
		t.Fatal(err)
	}

	opts = append(opts, oci.WithImageConfig(image))
	opts = append(opts, withProcessArgs("sh", "-c", "sleep 42; echo hi"))

	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(opts...),
	)
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

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
	}

	// Give the shim time to reap the init process and kill the orphans
	select {
	case <-statusC:
	case <-time.After(100 * time.Millisecond):
	}

	b, err := exec.Command("ps", "ax").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(b), "sleep 42") {
		t.Fatalf("killing init didn't kill all its children:\n%v", string(b))
	}

	if _, err := task.Delete(ctx, WithProcessKill); err != nil {
		t.Error(err)
	}
}

func TestContainerKillInitPidHost(t *testing.T) {
	initContainerAndCheckChildrenDieOnKill(t, oci.WithHostNamespace(specs.PIDNamespace))
}

func TestContainerKillInitKillsChildWhenNotHostPid(t *testing.T) {
	initContainerAndCheckChildrenDieOnKill(t)
}

func TestUserNamespaces(t *testing.T) {
	t.Parallel()
	t.Run("WritableRootFS", func(t *testing.T) { testUserNamespaces(t, false) })
	// see #1373 and runc#1572
	t.Run("ReadonlyRootFS", func(t *testing.T) { testUserNamespaces(t, true) })
}

func checkUserNS(t *testing.T) {
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER,
	}

	if err := cmd.Run(); err != nil {
		t.Skip("User namespaces are unavailable")
	}
}

func testUserNamespaces(t *testing.T, readonlyRootFS bool) {
	checkUserNS(t)

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

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	opts := []NewContainerOpts{WithNewSpec(oci.WithImageConfig(image),
		withExitStatus(7),
		oci.WithUserNamespace(0, 1000, 10000),
	)}
	if readonlyRootFS {
		opts = append([]NewContainerOpts{WithRemappedSnapshotView(id, image, 1000, 1000)}, opts...)
	} else {
		opts = append([]NewContainerOpts{WithRemappedSnapshot(id, image, 1000, 1000)}, opts...)
	}

	container, err := client.NewContainer(ctx, id, opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	var copts interface{}
	if client.runtime == "io.containerd.runc.v1" {
		copts = &options.Options{
			IoUid: 1000,
			IoGid: 1000,
		}
	} else {
		copts = &runctypes.CreateOptions{
			IoUid: 1000,
			IoGid: 1000,
		}
	}

	task, err := container.NewTask(ctx, cio.NewCreator(cio.WithStdio), func(_ context.Context, client *Client, r *TaskInfo) error {
		r.Options = copts
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if pid := task.Pid(); pid < 1 {
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

func TestTaskResize(t *testing.T) {
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
	if err := task.Resize(ctx, 32, 32); err != nil {
		t.Fatal(err)
	}
	task.Kill(ctx, syscall.SIGKILL)
	<-statusC
}

func TestContainerImage(t *testing.T) {
	t.Parallel()

	ctx, cancel := testContext()
	defer cancel()
	id := t.Name()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSpec(), WithImage(image))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx)

	i, err := container.Image(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if i.Name() != image.Name() {
		t.Fatalf("expected container image name %s but received %s", image.Name(), i.Name())
	}
}

func TestContainerNoImage(t *testing.T) {
	t.Parallel()

	ctx, cancel := testContext()
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

	_, err = container.Image(ctx)
	if err == nil {
		t.Fatal("error should not be nil when container is created without an image")
	}
	if errors.Cause(err) != errdefs.ErrNotFound {
		t.Fatalf("expected error to be %s but received %s", errdefs.ErrNotFound, err)
	}
}

func TestUIDNoGID(t *testing.T) {
	t.Parallel()

	ctx, cancel := testContext()
	defer cancel()
	id := t.Name()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithUserID(1000)))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx)

	spec, err := container.Spec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if uid := spec.Process.User.UID; uid != 1000 {
		t.Fatalf("expected uid 1000 but received %d", uid)
	}
	if gid := spec.Process.User.GID; gid != 0 {
		t.Fatalf("expected gid 0 but received %d", gid)
	}
}

func TestBindLowPortNonRoot(t *testing.T) {
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

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), withProcessArgs("nc", "-l", "-p", "80"), oci.WithUIDGID(1000, 1000)),
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
	status := <-statusC
	code, _, err := status.Result()
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Errorf("expected status 1 from wait but received %d", code)
	}
	if _, err := task.Delete(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestBindLowPortNonOpt(t *testing.T) {
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

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), withProcessArgs("nc", "-l", "-p", "80"), oci.WithUIDGID(1000, 1000), oci.WithAmbientCapabilities([]string{"CAP_NET_BIND_SERVICE"})),
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
	go func() {
		time.Sleep(2 * time.Second)
		task.Kill(ctx, unix.SIGTERM)
	}()
	status := <-statusC
	code, _, err := status.Result()
	if err != nil {
		t.Fatal(err)
	}
	// 128 + sigterm
	if code != 143 {
		t.Errorf("expected status 143 from wait but received %d", code)
	}
	if _, err := task.Delete(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestContainerNoSTDIN(t *testing.T) {
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

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), withExitStatus(0)))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, cio.NewCreator(cio.WithStreams(nil, ioutil.Discard, ioutil.Discard)))
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
		t.Errorf("expected status 0 from wait but received %d", code)
	}
}
