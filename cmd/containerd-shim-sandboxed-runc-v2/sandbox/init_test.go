//go:build linux

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

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gorunc "github.com/containerd/go-runc"
	"github.com/moby/sys/mountinfo"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/sys/reaper"
	"github.com/containerd/containerd/v2/pkg/testutil"
)

func TestCgroupsPath(t *testing.T) {
	assert.Equal(t, "/kubepods/burstable/pod1/"+testID, cgroupsPath("/kubepods/burstable/pod1", testID))
	assert.Equal(t, "kubepods-burstable-pod1.slice:cri-containerd:"+testID, cgroupsPath("/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod1.slice", testID))
	assert.Equal(t, "test.slice:cri-containerd:"+testID, cgroupsPath("/test.slice", testID))
}

func TestInitSpec(t *testing.T) {
	cfg := &Config{ID: testID, NetNSPath: testNetNS, CgroupParent: "/kubepods/pod1", SharedPID: true}
	pins := Pins{IPC: "/bundle/ns/ipc", UTS: "/bundle/ns/uts"}
	binary := "/usr/local/bin/" + InitBinary
	spec := initSpec(cfg, pins, binary)

	require.NotNil(t, spec.Process)
	assert.Equal(t, []string{"/" + InitBinary}, spec.Process.Args)
	assert.Equal(t, specs.User{UID: initUser, GID: initUser}, spec.Process.User)
	assert.True(t, spec.Process.NoNewPrivileges)
	assert.Equal(t, &specs.LinuxCapabilities{}, spec.Process.Capabilities, "no capabilities")
	require.NotNil(t, spec.Process.OOMScoreAdj)
	assert.Equal(t, initOOMScoreAdj, *spec.Process.OOMScoreAdj)
	assert.Equal(t, &specs.Root{Path: "rootfs", Readonly: true}, spec.Root)
	assert.Equal(t, specs.Mount{Destination: "/" + InitBinary, Type: "bind", Source: binary, Options: []string{"bind", "ro", "nosuid", "nodev"}}, spec.Mounts[len(spec.Mounts)-1])
	assert.Equal(t, []specs.LinuxNamespace{
		{Type: specs.PIDNamespace},
		{Type: specs.MountNamespace},
		{Type: specs.NetworkNamespace, Path: testNetNS},
		{Type: specs.IPCNamespace, Path: pins.IPC},
		{Type: specs.UTSNamespace, Path: pins.UTS},
	}, spec.Linux.Namespaces)
	assert.Equal(t, "/kubepods/pod1/"+testID, spec.Linux.CgroupsPath)
	assert.Empty(t, spec.Hostname, "the hostname of the joined UTS namespace is left alone")

	t.Run("host namespaces", func(t *testing.T) {
		spec := initSpec(&Config{ID: testID, HostNetwork: true, HostIPC: true, SharedPID: true}, Pins{}, binary)
		assert.Equal(t, []specs.LinuxNamespace{{Type: specs.PIDNamespace}, {Type: specs.MountNamespace}}, spec.Linux.Namespaces)
		assert.Empty(t, spec.Linux.CgroupsPath)
	})
}

// startReaper makes the test process reap its children the way the shim
// does; the runc commands and the exit tracking of the init rely on it.
func startReaper(t *testing.T) {
	require.NoError(t, reaper.SetSubreaper(1))
	monitor := gorunc.Monitor
	gorunc.Monitor = reaper.Default
	ch := make(chan os.Signal, 32)
	signal.Notify(ch, unix.SIGCHLD)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				_ = reaper.Reap()
			case <-done:
				return
			}
		}
	}()
	t.Cleanup(func() {
		signal.Stop(ch)
		close(done)
		gorunc.Monitor = monitor
		_ = reaper.SetSubreaper(0)
	})
}

// newInit creates the init of a pod in the host network and IPC namespaces
// in a fresh sandbox bundle, stopped and cleaned up with the test. The test
// needs root, runc and the installed init binary.
func newInit(t *testing.T) (context.Context, string, *initProcess) {
	testutil.RequiresRoot(t)
	if _, err := exec.LookPath("runc"); err != nil {
		t.Skip("runc is not installed")
	}
	if _, err := initBinary(); err != nil {
		t.Skip(err.Error())
	}
	startReaper(t)

	ctx := namespaces.WithNamespace(context.Background(), "testing")
	bundle := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(bundle, initRootfs), 0o711))
	t.Cleanup(func() { _ = Cleanup(ctx, bundle) })
	cfg := &Config{ID: fmt.Sprintf("sandbox-init-test-%d", os.Getpid()), HostNetwork: true, HostIPC: true, SharedPID: true, CgroupParent: "/containerd-test"}
	p, err := createInit(ctx, bundle, cfg, Pins{})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = p.stop(ctx) })
	return ctx, bundle, p
}

// startInit execs the program of p and waits for it to run.
func startInit(ctx context.Context, t *testing.T, p *initProcess) {
	require.NoError(t, p.runtime.Start(ctx, p.id))
	require.Eventually(t, func() bool { return running(p.pid) }, 5*time.Second, 20*time.Millisecond)
}

// running reports whether the init program is running as pid: the comm of
// the process is the binary name, truncated by the kernel.
func running(pid int) bool {
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	return err == nil && strings.HasPrefix(InitBinary, strings.TrimSpace(string(comm)))
}

func TestInitLifecycle(t *testing.T) {
	ctx, bundle, p := newInit(t)
	assert.Greater(t, p.pid, 0)
	mounted, err := mountinfo.Mounted(p.pidNS)
	require.NoError(t, err)
	assert.True(t, mounted, "the PID namespace is pinned")
	assert.False(t, running(p.pid), "created, not started")

	startInit(ctx, t, p)

	exit, err := p.stop(ctx)
	require.NoError(t, err)
	assert.Equal(t, 128+int(unix.SIGKILL), exit.Status)
	assert.NoFileExists(t, fmt.Sprintf("/proc/%d", p.pid))

	// Stopping again deletes what is already deleted and reports the same exit.
	again, err := p.stop(ctx)
	require.NoError(t, err)
	assert.Equal(t, exit, again)

	require.NoError(t, Cleanup(ctx, bundle))
	for _, p := range []string{filepath.Join(bundle, initRootfs), p.pidNS} {
		mounted, err := mountinfo.Mounted(p)
		if err == nil {
			assert.False(t, mounted, "%s is detached", p)
		}
	}
}

func TestInitExit(t *testing.T) {
	ctx, _, p := newInit(t)
	startInit(ctx, t, p)

	// An init killed from outside the shim is noticed and its exit kept.
	require.NoError(t, unix.Kill(p.pid, unix.SIGKILL))
	select {
	case <-p.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the exit of the init was not seen")
	}
	exit, err := p.stop(ctx)
	require.NoError(t, err)
	assert.Equal(t, 128+int(unix.SIGKILL), exit.Status)
}
