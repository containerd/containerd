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
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gorunc "github.com/containerd/go-runc"
	"github.com/moby/sys/mountinfo"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/process"
	"github.com/containerd/containerd/v2/contrib/seccomp"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/netns"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/containerd/v2/pkg/sys/reaper"
	"github.com/containerd/containerd/v2/pkg/testutil"
)

func TestCgroupsPath(t *testing.T) {
	assert.Equal(t, "/kubepods/burstable/pod1/"+testID, cgroupsPath("/kubepods/burstable/pod1", testID))
	assert.Equal(t, "kubepods-burstable-pod1.slice:cri-containerd:"+testID, cgroupsPath("/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod1.slice", testID))
	assert.Equal(t, "test.slice:cri-containerd:"+testID, cgroupsPath("/test.slice", testID))
}

func TestRuncSpec(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "testing")
	binary := "/usr/local/bin/" + InitBinary
	sysctls := map[string]string{"net.ipv4.ip_forward": "1", "kernel.shm_rmid_forced": "1"}
	cfg := &Config{ID: testID, Hostname: "pod", NetNSPath: testNetNS, CgroupParent: "/kubepods/pod1", Sysctls: sysctls}
	spec, err := runcSpec(ctx, cfg, binary)
	require.NoError(t, err)

	require.NotNil(t, spec.Process)
	assert.Equal(t, []string{"/" + InitBinary}, spec.Process.Args)
	assert.Equal(t, specs.User{UID: initUser, GID: initUser}, spec.Process.User)
	assert.True(t, spec.Process.NoNewPrivileges)
	assert.Equal(t, &specs.LinuxCapabilities{}, spec.Process.Capabilities, "no capabilities")
	require.NotNil(t, spec.Process.OOMScoreAdj)
	assert.Equal(t, initOOMScoreAdj, *spec.Process.OOMScoreAdj)
	assert.Equal(t, &specs.Root{Path: runcRootfs, Readonly: true}, spec.Root)
	assert.Equal(t, specs.Mount{Destination: "/" + InitBinary, Type: "bind", Source: binary, Options: []string{"bind", "ro", "nosuid", "nodev"}}, spec.Mounts[len(spec.Mounts)-1])
	// The container creates the IPC and UTS namespaces and joins the CRI
	// network namespace, where runc sets the hostname and the sysctls.
	assert.Equal(t, []specs.LinuxNamespace{
		{Type: specs.MountNamespace},
		{Type: specs.NetworkNamespace, Path: testNetNS},
		{Type: specs.IPCNamespace},
		{Type: specs.UTSNamespace},
	}, spec.Linux.Namespaces)
	assert.Equal(t, "pod", spec.Hostname)
	assert.Equal(t, sysctls, spec.Linux.Sysctl)
	assert.Equal(t, "/kubepods/pod1/"+testID, spec.Linux.CgroupsPath)
	defaults, err := oci.GenerateSpec(ctx, nil, &containers.Container{ID: testID})
	require.NoError(t, err)
	assert.Equal(t, defaults.Linux.MaskedPaths, spec.Linux.MaskedPaths)
	assert.Equal(t, defaults.Linux.ReadonlyPaths, spec.Linux.ReadonlyPaths)
	assert.Contains(t, spec.Linux.ReadonlyPaths, "/proc/sys")
	assert.Nil(t, spec.Linux.Seccomp, "the program of a container that is never started needs no profile")

	t.Run("shared PID namespace", func(t *testing.T) {
		cfg := *cfg
		cfg.SharedPID = true
		spec, err := runcSpec(ctx, &cfg, binary)
		require.NoError(t, err)
		assert.Equal(t, specs.LinuxNamespace{Type: specs.PIDNamespace}, spec.Linux.Namespaces[len(spec.Linux.Namespaces)-1])
		assert.Equal(t, seccomp.DefaultProfile(spec), spec.Linux.Seccomp, "a started container runs under the default profile")
	})

	t.Run("host namespaces", func(t *testing.T) {
		spec, err := runcSpec(ctx, &Config{ID: testID, Hostname: "node", HostNetwork: true, HostIPC: true, SharedPID: true}, binary)
		require.NoError(t, err)
		assert.Equal(t, []specs.LinuxNamespace{{Type: specs.MountNamespace}, {Type: specs.PIDNamespace}}, spec.Linux.Namespaces)
		assert.Empty(t, spec.Hostname, "the host UTS namespace keeps its hostname")
		assert.Empty(t, spec.Linux.CgroupsPath)
	})
}

// startReaper makes the test process reap its children as the shim does; the
// runc commands and the exit tracking of the container rely on it.
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

var setupSeq atomic.Int32

// newPod prepares cfg and a fresh sandbox bundle for a setup under a cgroup
// parent of its own, and cleans the bundle up with the test. The test needs
// root, runc and, for a shared-PID pod, the installed init binary.
func newPod(t *testing.T, cfg *Config) (context.Context, string) {
	testutil.RequiresRoot(t)
	if _, err := exec.LookPath("runc"); err != nil {
		t.Skip("runc is not installed")
	}
	if _, err := initBinary(); err != nil && cfg.SharedPID {
		t.Skip(err.Error())
	}
	startReaper(t)

	ctx := namespaces.WithNamespace(context.Background(), "testing")
	cfg.ID = fmt.Sprintf("sandbox-test-%d-%d", os.Getpid(), setupSeq.Add(1))
	cfg.CgroupParent = "/" + cfg.ID
	// runc removes the container cgroup, not its parent.
	t.Cleanup(func() { _ = os.Remove(filepath.Join("/sys/fs/cgroup", cfg.CgroupParent)) })
	bundle := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(ctx, bundle) })
	return ctx, bundle
}

// setupPod sets the namespaces of cfg up in a new pod (see newPod), and stops
// what is left with the test.
func setupPod(t *testing.T, cfg *Config) (context.Context, string, Pins, *initProcess) {
	ctx, bundle := newPod(t, cfg)
	pins, c, err := setupNamespaces(ctx, bundle, cfg)
	require.NoError(t, err)
	if c != nil {
		t.Cleanup(func() { _, _ = c.stop(ctx) })
	}
	return ctx, bundle, pins, c
}

// newInit creates the container of a shared-PID pod in the host network and
// IPC namespaces.
func newInit(t *testing.T) (context.Context, string, Pins, *initProcess) {
	ctx, bundle, pins, c := setupPod(t, &Config{HostNetwork: true, HostIPC: true, SharedPID: true})
	require.NotNil(t, c)
	return ctx, bundle, pins, c
}

// startInit execs the program of c and waits for it to run.
func startInit(ctx context.Context, t *testing.T, c *initProcess) {
	require.NoError(t, c.runtime.Start(ctx, c.id))
	require.Eventually(t, func() bool { return running(c.pid) }, 5*time.Second, 20*time.Millisecond)
}

// running reports whether the init program is running as pid: the comm of
// the process is the binary name, truncated by the kernel.
func running(pid int) bool {
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	return err == nil && strings.HasPrefix(InitBinary, strings.TrimSpace(string(comm)))
}

// TestSetupNamespaces sets an ordinary pod up: runc creates the namespaces,
// the shim pins them and deletes the container.
func TestSetupNamespaces(t *testing.T) {
	testutil.RequiresRoot(t)
	podNetNS, err := netns.NewNetNS(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = podNetNS.Remove() })

	cfg := &Config{
		Hostname:  "sandbox-test",
		NetNSPath: podNetNS.GetPath(),
		Sysctls:   map[string]string{"kernel.shm_rmid_forced": "1", "net.ipv4.ip_unprivileged_port_start": "0"},
	}
	hostname, err := os.Hostname()
	require.NoError(t, err)
	ctx, bundle, pins, c := setupPod(t, cfg)
	assert.Nil(t, c, "nothing stays running for an ordinary pod")
	assert.Equal(t, Pins{IPC: filepath.Join(bundle, pinDir, "ipc"), UTS: filepath.Join(bundle, pinDir, "uts")}, pins)

	// No container remains; the pins keep the namespaces alive.
	_, err = process.NewRunc("", t.TempDir(), "testing", "runc", false).State(ctx, cfg.ID)
	assert.Error(t, err, "the runc container is deleted")
	assert.NoDirExists(t, filepath.Join("/sys/fs/cgroup", cfg.CgroupParent, cfg.ID), "its cgroup is removed")
	for _, tc := range []struct{ pin, nsType string }{{pins.IPC, "ipc"}, {pins.UTS, "uts"}} {
		var st unix.Statfs_t
		require.NoError(t, unix.Statfs(tc.pin, &st))
		assert.EqualValues(t, unix.NSFS_MAGIC, st.Type, "%s pin is an nsfs mount", tc.nsType)
		assert.NotEqual(t, inode(t, "/proc/self/ns/"+tc.nsType), inode(t, tc.pin), "%s namespace is not the host one", tc.nsType)
	}

	// runc set the hostname and the sysctls in the pod namespaces.
	assert.Equal(t, "sandbox-test", inNamespace(t, pins.UTS, unix.CLONE_NEWUTS, func() (string, error) {
		var u unix.Utsname
		err := unix.Uname(&u)
		return unix.ByteSliceToString(u.Nodename[:]), err
	}))
	readSysctl := func(file string) func() (string, error) {
		return func() (string, error) {
			b, err := os.ReadFile(file)
			return strings.TrimSpace(string(b)), err
		}
	}
	assert.Equal(t, "1", inNamespace(t, pins.IPC, unix.CLONE_NEWIPC, readSysctl("/proc/sys/kernel/shm_rmid_forced")))
	assert.Equal(t, "0", inNamespace(t, podNetNS.GetPath(), unix.CLONE_NEWNET, readSysctl("/proc/sys/net/ipv4/ip_unprivileged_port_start")))
	current, err := os.Hostname()
	require.NoError(t, err)
	assert.Equal(t, hostname, current, "the host keeps its hostname")

	// Cleanup unmounts the pins and removes them and the container files; a
	// second run is a no-op.
	require.NoError(t, Cleanup(ctx, bundle))
	for _, p := range []string{filepath.Join(bundle, pinDir), filepath.Join(bundle, runcDir)} {
		assert.NoDirExists(t, p)
	}
	assertNoMountsUnder(t, bundle)
	require.NoError(t, Cleanup(ctx, bundle))
}

// TestSetupNamespacesFailure has runc reject a sysctl value.
func TestSetupNamespacesFailure(t *testing.T) {
	cfg := &Config{HostNetwork: true, Sysctls: map[string]string{"kernel.msgmax": "invalid"}}
	ctx, bundle := newPod(t, cfg)
	_, c, err := setupNamespaces(ctx, bundle, cfg)
	require.Error(t, err)
	assert.Nil(t, c)
	assert.Contains(t, err.Error(), "kernel.msgmax", "the error carries the reason runc logged")
	_, err = process.NewRunc("", t.TempDir(), "testing", "runc", false).State(ctx, cfg.ID)
	assert.Error(t, err, "the runc container is deleted")
	assert.NoDirExists(t, filepath.Join("/sys/fs/cgroup", cfg.CgroupParent, cfg.ID), "its cgroup is removed")
}

func TestSetupNamespacesHostModes(t *testing.T) {
	testutil.RequiresRoot(t)

	// A pod in the host network and IPC namespaces that does not share its
	// PID namespace does not get a namespace of its own, and the shim does
	// not run runc for it.
	bundle := t.TempDir()
	pins, c, err := setupNamespaces(context.Background(), bundle, &Config{ID: testID, HostNetwork: true, HostIPC: true})
	require.NoError(t, err)
	assert.Nil(t, c)
	assert.Equal(t, Pins{}, pins)
	assert.NoDirExists(t, filepath.Join(bundle, runcDir))
	assert.NoDirExists(t, filepath.Join(bundle, pinDir))

	// A pod in the host network with its own IPC namespace gets only the IPC
	// namespace.
	_, bundle, pins, _ = setupPod(t, &Config{HostNetwork: true})
	assert.Equal(t, Pins{IPC: filepath.Join(bundle, pinDir, "ipc")}, pins)
	assert.NoFileExists(t, filepath.Join(bundle, pinDir, "uts"))
}

func TestInitLifecycle(t *testing.T) {
	ctx, bundle, pins, c := newInit(t)
	assert.Greater(t, c.pid, 0)
	mounted, err := mountinfo.Mounted(pins.PID)
	require.NoError(t, err)
	assert.True(t, mounted, "the PID namespace is pinned")
	assert.False(t, running(c.pid), "created, not started")

	startInit(ctx, t, c)

	exit, err := c.stop(ctx)
	require.NoError(t, err)
	assert.Equal(t, 128+int(unix.SIGKILL), exit.Status)
	assert.NoFileExists(t, fmt.Sprintf("/proc/%d", c.pid))

	// Stopping again deletes what is already deleted and reports the same exit.
	again, err := c.stop(ctx)
	require.NoError(t, err)
	assert.Equal(t, exit, again)

	require.NoError(t, Cleanup(ctx, bundle))
	assert.NoFileExists(t, pins.PID, "the PID namespace pin is unmounted and removed")
}

func TestInitExit(t *testing.T) {
	ctx, _, _, c := newInit(t)
	startInit(ctx, t, c)

	// The shim notices an init killed from outside and keeps its exit.
	require.NoError(t, unix.Kill(c.pid, unix.SIGKILL))
	select {
	case <-c.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the exit of the init was not seen")
	}
	exit, err := c.stop(ctx)
	require.NoError(t, err)
	assert.Equal(t, 128+int(unix.SIGKILL), exit.Status)
}

// inNamespace runs fn on a thread that entered the namespace pinned at pin
// and returns what it reads. The thread is locked and discarded afterwards.
func inNamespace(t *testing.T, pin string, nsType int, fn func() (string, error)) string {
	t.Helper()
	var (
		result string
		err    error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		var fd int
		if fd, err = unix.Open(pin, unix.O_RDONLY|unix.O_CLOEXEC, 0); err != nil {
			return
		}
		defer unix.Close(fd)
		if err = unix.Setns(fd, nsType); err != nil {
			return
		}
		result, err = fn()
	}()
	<-done
	require.NoError(t, err, "in namespace %s", pin)
	return result
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	var st unix.Stat_t
	require.NoError(t, unix.Stat(path, &st))
	return st.Ino
}

func assertNoMountsUnder(t *testing.T, dir string) {
	t.Helper()
	mounts, err := mountinfo.GetMounts(mountinfo.PrefixFilter(dir))
	require.NoError(t, err)
	assert.Empty(t, mounts, "no mounts left under %s", dir)
}
