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
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	apitaskv2 "github.com/containerd/containerd/api/runtime/task/v2"
	"github.com/containerd/containerd/integration/images"
	"github.com/containerd/containerd/namespaces"
	apitaskv1 "github.com/containerd/containerd/runtime/v1/shim/v1"
	"github.com/containerd/containerd/runtime/v2/shim"
	"github.com/containerd/ttrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIssue7496 is used to reproduce https://github.com/containerd/containerd/issues/7496
//
// NOTE: https://github.com/containerd/containerd/issues/8931 is the same issue.
func TestIssue7496(t *testing.T) {
	t.Logf("Checking CRI config's default runtime")
	criCfg, err := CRIConfig()
	require.NoError(t, err)

	typ := criCfg.ContainerdConfig.Runtimes[criCfg.ContainerdConfig.DefaultRuntimeName].Type
	isShimV1 := typ == "io.containerd.runtime.v1.linux"

	ctx := namespaces.WithNamespace(context.Background(), "k8s.io")

	t.Logf("Create a pod config and run sandbox container")
	sbConfig := PodSandboxConfig("sandbox", "issue7496")
	sbID, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
	require.NoError(t, err)

	sCli := newShimCli(ctx, t, sbID, isShimV1)

	delayInSec := 12
	t.Logf("[shim pid: %d]: Injecting %d seconds delay to umount2 syscall",
		sCli.pid(ctx, t),
		delayInSec)

	doneCh := injectDelayToUmount2(ctx, t, int(sCli.pid(ctx, t)), delayInSec /* CRI plugin uses 10 seconds to delete task */)

	t.Logf("Create a container config and run container in a pod")
	pauseImage := images.Get(images.Pause)
	EnsureImageExists(t, pauseImage)

	containerConfig := ContainerConfig("pausecontainer", pauseImage)
	cnID, err := runtimeService.CreateContainer(sbID, containerConfig, sbConfig)
	require.NoError(t, err)
	require.NoError(t, runtimeService.StartContainer(cnID))

	t.Logf("Start to StopPodSandbox and RemovePodSandbox")
	ctx, cancelFn := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelFn()
	for {
		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err(), "The StopPodSandbox should be done in time")
		default:
		}

		err := runtimeService.StopPodSandbox(sbID)
		if err != nil {
			t.Logf("Failed to StopPodSandbox: %v", err)
			continue
		}

		err = runtimeService.RemovePodSandbox(sbID)
		if err == nil {
			break
		}
		t.Logf("Failed to RemovePodSandbox: %v", err)
		time.Sleep(1 * time.Second)
	}

	t.Logf("PodSandbox %s has been deleted and start to wait for strace exit", sbID)
	select {
	case <-time.After(15 * time.Second):
		shimPid, err := sCli.connect(ctx)
		assert.Error(t, err, "should failed to call shim connect API")

		t.Errorf("Strace doesn't exit in time")

		t.Logf("Cleanup the shim (pid: %d)", shimPid)
		syscall.Kill(int(shimPid), syscall.SIGKILL)
		<-doneCh
	case <-doneCh:
	}
}

// injectDelayToUmount2 uses strace(1) to inject delay on umount2 syscall to
// simulate IO pressure because umount2 might force kernel to syncfs, for
// example, umount overlayfs rootfs which doesn't with volatile.
//
// REF: https://man7.org/linux/man-pages/man1/strace.1.html
func injectDelayToUmount2(ctx context.Context, t *testing.T, shimPid, delayInSec int) chan struct{} {
	doneCh := make(chan struct{})

	cmd := exec.CommandContext(ctx, "strace",
		"-p", strconv.Itoa(shimPid), "-f", // attach to all the threads
		"--detach-on=execve", // stop to attach runc child-processes
		"--trace=umount2",    // only trace umount2 syscall
		"-e", "inject=umount2:delay_enter="+strconv.Itoa(delayInSec)+"s",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}

	pipeR, pipeW := io.Pipe()
	cmd.Stdout = pipeW
	cmd.Stderr = pipeW

	require.NoError(t, cmd.Start())

	// ensure that strace has attached to the shim
	readyCh := make(chan struct{})
	go func() {
		defer close(doneCh)

		bufReader := bufio.NewReader(pipeR)
		_, err := bufReader.Peek(1)
		assert.NoError(t, err, "failed to ensure that strace has attached to shim")

		close(readyCh)
		io.Copy(os.Stdout, bufReader)
		t.Logf("Strace has exited")
	}()

	go func() {
		defer pipeW.Close()
		assert.NoError(t, cmd.Wait(), "strace should exit with zero code")
	}()

	<-readyCh
	return doneCh
}

type shimCli struct {
	isV1 bool

	cliV1 apitaskv1.ShimService
	cliV2 apitaskv2.TaskService
}

func newShimCli(ctx context.Context, t *testing.T, id string, isV1 bool) *shimCli {
	addr, err := shim.SocketAddress(ctx, containerdEndpoint, id)
	require.NoError(t, err)
	addr = strings.TrimPrefix(addr, "unix://")

	conn, err := net.Dial("unix", addr)
	require.NoError(t, err)

	client := ttrpc.NewClient(conn)

	cli := &shimCli{isV1: isV1}
	if isV1 {
		cli.cliV1 = apitaskv1.NewShimClient(client)
	} else {
		cli.cliV2 = apitaskv2.NewTaskClient(client)
	}
	return cli
}

func (cli *shimCli) connect(ctx context.Context) (uint32, error) {
	if cli.isV1 {
		resp, err := cli.cliV1.ShimInfo(ctx, nil)
		if err != nil {
			return 0, err
		}
		return resp.GetShimPid(), nil
	}

	resp, err := cli.cliV2.Connect(ctx, nil)
	if err != nil {
		return 0, err
	}
	return resp.GetShimPid(), nil
}

func (cli *shimCli) pid(ctx context.Context, t *testing.T) uint32 {
	pid, err := cli.connect(ctx)
	require.NoError(t, err)
	return pid
}
