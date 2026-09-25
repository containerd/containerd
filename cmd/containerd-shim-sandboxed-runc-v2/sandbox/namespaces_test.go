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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/moby/sys/mountinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/pkg/testutil"
)

// inNamespace runs fn on a thread that entered the namespace pinned at pin
// and returns what it reads. The thread is locked and discarded afterwards,
// like the one that created the namespace.
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

func TestSetupNamespaces(t *testing.T) {
	testutil.RequiresRoot(t)
	ctx := context.Background()
	bundle := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(ctx, bundle) })

	pins, err := setupNamespaces(bundle, &Config{
		Hostname: "sandbox-test",
		Sysctls:  map[string]string{"kernel.shm_rmid_forced": "1"},
	})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(bundle, pinDir, ipcPin), pins.IPC)
	assert.Equal(t, filepath.Join(bundle, pinDir, utsPin), pins.UTS)

	// The pins are namespace files distinct from the host namespaces.
	for _, tc := range []struct{ pin, nsType string }{{pins.IPC, "ipc"}, {pins.UTS, "uts"}} {
		var st unix.Statfs_t
		require.NoError(t, unix.Statfs(tc.pin, &st))
		assert.EqualValues(t, unix.NSFS_MAGIC, st.Type, "%s pin is an nsfs mount", tc.nsType)
		assert.NotEqual(t, inode(t, "/proc/self/ns/"+tc.nsType), inode(t, tc.pin), "%s namespace is not the host one", tc.nsType)
	}

	// The pod UTS namespace carries the pod hostname; the host keeps its own.
	hostHostname, err := os.Hostname()
	require.NoError(t, err)
	podHostname := inNamespace(t, pins.UTS, unix.CLONE_NEWUTS, func() (string, error) {
		var u unix.Utsname
		if err := unix.Uname(&u); err != nil {
			return "", err
		}
		return unix.ByteSliceToString(u.Nodename[:]), nil
	})
	assert.Equal(t, "sandbox-test", podHostname)
	current, err := os.Hostname()
	require.NoError(t, err)
	assert.Equal(t, hostHostname, current)

	// The IPC sysctl was applied inside the pod IPC namespace.
	value := inNamespace(t, pins.IPC, unix.CLONE_NEWIPC, func() (string, error) {
		b, err := os.ReadFile("/proc/sys/kernel/shm_rmid_forced")
		return strings.TrimSpace(string(b)), err
	})
	assert.Equal(t, "1", value)

	// Cleanup detaches the pins and removes them; a second run is a no-op.
	require.NoError(t, Cleanup(ctx, bundle))
	for _, p := range []string{pins.IPC, pins.UTS, filepath.Join(bundle, pinDir)} {
		_, err := os.Stat(p)
		assert.True(t, os.IsNotExist(err), "%s should be gone", p)
	}
	assertNoMountsUnder(t, bundle)
	require.NoError(t, Cleanup(ctx, bundle))
}

func TestSetupNamespacesHostModes(t *testing.T) {
	testutil.RequiresRoot(t)
	bundle := t.TempDir()

	// A pod in the host network and IPC namespaces holds nothing.
	pins, err := setupNamespaces(bundle, &Config{HostNetwork: true, HostIPC: true})
	require.NoError(t, err)
	assert.Equal(t, Pins{}, pins)
	_, err = os.Stat(filepath.Join(bundle, pinDir))
	assert.True(t, os.IsNotExist(err), "no pin directory without pins")

	// Host network with a pod IPC namespace: only the IPC pin exists.
	pins, err = setupNamespaces(bundle, &Config{HostNetwork: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = Cleanup(context.Background(), bundle) })
	assert.NotEmpty(t, pins.IPC)
	assert.Empty(t, pins.UTS)
	_, err = os.Stat(filepath.Join(bundle, pinDir, utsPin))
	assert.True(t, os.IsNotExist(err))
}
