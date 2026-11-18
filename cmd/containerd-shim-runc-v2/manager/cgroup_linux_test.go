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

package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup1"
	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemdCgroup(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cgroupsPath string
		slice       string
		unit        string
		ok          bool
	}{{
		name:        "kubernetes besteffort pod",
		cgroupsPath: "kubepods-besteffort-podabc.slice:cri-containerd:deadbeef",
		slice:       "kubepods-besteffort-podabc.slice",
		unit:        "cri-containerd-deadbeef.scope",
		ok:          true,
	}, {
		name:        "empty slice",
		cgroupsPath: ":cri-containerd:deadbeef",
		slice:       "",
		unit:        "cri-containerd-deadbeef.scope",
		ok:          true,
	}, {
		name:        "cgroupfs path",
		cgroupsPath: "/kubepods/besteffort/podabc/deadbeef",
	}, {
		name:        "empty",
		cgroupsPath: "",
	}, {
		name:        "too few components",
		cgroupsPath: "system.slice:deadbeef",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			slice, unit, ok := systemdCgroup(tc.cgroupsPath)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.slice, slice)
			assert.Equal(t, tc.unit, unit)
		})
	}
}

func TestExpandSlice(t *testing.T) {
	for _, tc := range []struct {
		slice string
		path  string
	}{
		{slice: "", path: "system.slice"},
		{slice: "-.slice", path: ""},
		{slice: "system.slice", path: "system.slice"},
		{slice: "kubepods.slice", path: "kubepods.slice"},
		{slice: "kubepods-besteffort.slice", path: "kubepods.slice/kubepods-besteffort.slice"},
		{slice: "kubepods-besteffort-podabc.slice", path: "kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-podabc.slice"},
		// Not a slice, so it is used as is.
		{slice: "user-1000.scope", path: "user-1000.scope"},
	} {
		t.Run(tc.slice, func(t *testing.T) {
			assert.Equal(t, tc.path, expandSlice(tc.slice))
		})
	}
}

// writeBundleConfig writes a minimal OCI spec declaring cgroupsPath into a new
// bundle directory and returns its path.
func writeBundleConfig(t *testing.T, cgroupsPath string) string {
	t.Helper()

	bundle := t.TempDir()
	spec := map[string]any{"linux": map[string]any{"cgroupsPath": cgroupsPath}}
	b, err := json.Marshal(spec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(bundle, "config.json"), b, 0600))
	return bundle
}

func TestReadCgroupsPath(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		bundle := writeBundleConfig(t, "/a/b/c")
		path, err := readCgroupsPath(bundle)
		assert.NoError(t, err)
		assert.Equal(t, "/a/b/c", path)
	})

	t.Run("no linux section", func(t *testing.T) {
		bundle := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(bundle, "config.json"), []byte(`{}`), 0600))
		path, err := readCgroupsPath(bundle)
		assert.NoError(t, err)
		assert.Empty(t, path)
	})

	t.Run("missing config", func(t *testing.T) {
		_, err := readCgroupsPath(t.TempDir())
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
}

// testCgroup creates an empty cgroup v2 cgroup below the one the test process
// runs in, so that the test does not need to be able to write to the root of
// the hierarchy.
func testCgroup(t *testing.T) (*cgroupsv2.Manager, string) {
	t.Helper()

	if cgroups.Mode() != cgroups.Unified {
		t.Skip("test requires cgroup v2")
	}
	b, err := os.ReadFile("/proc/self/cgroup")
	require.NoError(t, err)
	// The unified hierarchy is the entry with an empty controller list.
	parent, ok := strings.CutPrefix(strings.SplitN(string(b), "\n", 2)[0], "0::")
	if !ok {
		t.Skip("test process is not in a cgroup v2 cgroup")
	}

	group := filepath.Join(parent, fmt.Sprintf("containerd-reap-test-%d", os.Getpid()))
	cg, err := cgroupsv2.NewManager(cgroup2Mountpoint, group, &cgroupsv2.Resources{})
	if errors.Is(err, os.ErrPermission) {
		t.Skipf("cgroup %s is not delegated to this process", parent)
	}
	require.NoError(t, err)
	t.Cleanup(func() {
		// Best effort: the cgroup is expected to be gone already.
		cg.Delete()
	})
	return cg, group
}

// TestReapContainerCgroup verifies that a container cgroup holding a live
// process is emptied and removed. This is the state a container is left in when
// its shim is killed without reaping it: "runc delete --force" fails, and
// without this cleanup the survivors hold the container rootfs busy forever.
func TestReapContainerCgroup(t *testing.T) {
	cg, group := testCgroup(t)

	cmd := exec.Command("sleep", "600")
	require.NoError(t, cmd.Start())
	// Reap the process once it is killed, as init would for an orphan.
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	require.NoError(t, cg.AddProc(uint64(cmd.Process.Pid)))
	procs, err := cg.Procs(true)
	require.NoError(t, err)
	require.Len(t, procs, 1, "the process should be in the cgroup")

	bundle := writeBundleConfig(t, group)
	require.NoError(t, reapContainerCgroup(context.Background(), bundle, false))

	assert.Error(t, <-waited, "the process should have been killed")
	_, err = os.Stat(filepath.Join(cgroup2Mountpoint, group))
	assert.ErrorIs(t, err, os.ErrNotExist, "the cgroup should have been removed")
}

// TestReapContainerCgroupEmpty verifies that an existing but empty cgroup, which
// is what is left once the processes have already gone away on their own, is
// simply removed.
func TestReapContainerCgroupEmpty(t *testing.T) {
	_, group := testCgroup(t)

	bundle := writeBundleConfig(t, group)
	assert.NoError(t, reapContainerCgroup(context.Background(), bundle, false))

	_, err := os.Stat(filepath.Join(cgroup2Mountpoint, group))
	assert.ErrorIs(t, err, os.ErrNotExist, "the cgroup should have been removed")
}

// TestReapContainerCgroupAlreadyGone verifies that reaping an already removed
// cgroup, which is the common case, is not an error.
func TestReapContainerCgroupAlreadyGone(t *testing.T) {
	if cgroups.Mode() != cgroups.Unified {
		t.Skip("test requires cgroup v2")
	}
	bundle := writeBundleConfig(t, fmt.Sprintf("/containerd-reap-missing-%d", os.Getpid()))
	assert.NoError(t, reapContainerCgroup(context.Background(), bundle, false))
}

// TestReapContainerCgroupNoCgroup verifies that a container without a dedicated
// cgroup is not an error.
func TestReapContainerCgroupNoCgroup(t *testing.T) {
	bundle := writeBundleConfig(t, "")
	assert.NoError(t, reapContainerCgroup(context.Background(), bundle, false))
}

// TestReapContainerCgroupRelative verifies that a relative cgroup path, which
// runc resolves against the cgroup it runs in, is not taken to be relative to
// the cgroup mount point: that would be some other cgroup.
func TestReapContainerCgroupRelative(t *testing.T) {
	cg, group := testCgroup(t)
	cmd, waited := startInCgroup(t, cg)

	bundle := writeBundleConfig(t, strings.TrimPrefix(group, "/"))
	require.NoError(t, reapContainerCgroup(context.Background(), bundle, false))

	select {
	case err := <-waited:
		t.Fatalf("the process should not have been killed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	procs, err := cg.Procs(true)
	require.NoError(t, err)
	assert.Len(t, procs, 1, "the cgroup should have been left alone")

	// Empty the cgroup so that it can be removed on cleanup.
	require.NoError(t, cmd.Process.Kill())
	<-waited
}

// TestReapContainerCgroupFrozen verifies that a frozen cgroup holding a live
// process is emptied and removed. This is the state a "runc create" that is
// killed while setting up the cgroup can leave behind, before runc has written
// any state that "runc delete" could use to clean it up.
func TestReapContainerCgroupFrozen(t *testing.T) {
	cg, group := testCgroup(t)

	cmd := exec.Command("sleep", "600")
	require.NoError(t, cmd.Start())
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	require.NoError(t, cg.AddProc(uint64(cmd.Process.Pid)))
	require.NoError(t, cg.Freeze())

	bundle := writeBundleConfig(t, group)
	require.NoError(t, reapContainerCgroup(context.Background(), bundle, false))

	assert.Error(t, <-waited, "the process should have been killed")
	_, err := os.Stat(filepath.Join(cgroup2Mountpoint, group))
	assert.ErrorIs(t, err, os.ErrNotExist, "the cgroup should have been removed")
}

func TestContainerCgroupPath(t *testing.T) {
	for _, tc := range []struct {
		cgroupsPath string
		systemd     bool
		path        string
	}{
		// cgroupfs notation is used as is.
		{cgroupsPath: "/leakrepro/abc", path: "/leakrepro/abc"},
		// Without the systemd cgroup driver, colons are just part of the path.
		{cgroupsPath: "/containers:legacy:abc", path: "/containers:legacy:abc"},
		{cgroupsPath: "system.slice:containerd:abc", path: "system.slice:containerd:abc"},
		// systemd notation.
		{cgroupsPath: "system.slice:containerd:abc", systemd: true, path: "/system.slice/containerd-abc.scope"},
		{cgroupsPath: ":cri-containerd:abc", systemd: true, path: "/system.slice/cri-containerd-abc.scope"},
		{
			cgroupsPath: "kubepods-besteffort-pod1.slice:cri-containerd:abc",
			systemd:     true,
			path:        "/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod1.slice/cri-containerd-abc.scope",
		},
		// The root slice: the scope sits directly below the cgroup mount point.
		// cgroup2.LoadSystemd would resolve this to /.slice/-.slice/...
		{cgroupsPath: "-.slice:cri-containerd:abc", systemd: true, path: "/cri-containerd-abc.scope"},
	} {
		t.Run(fmt.Sprintf("%s/systemd=%t", tc.cgroupsPath, tc.systemd), func(t *testing.T) {
			assert.Equal(t, tc.path, containerCgroupPath(tc.cgroupsPath, tc.systemd))
		})
	}
}

// startInCgroup starts a process in cg and returns it, along with a channel
// receiving its exit once it has been reaped.
func startInCgroup(t *testing.T, cg *cgroupsv2.Manager) (*exec.Cmd, <-chan error) {
	t.Helper()

	cmd := exec.Command("sleep", "600")
	require.NoError(t, cmd.Start())
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	t.Cleanup(func() { cmd.Process.Kill() })
	if cg != nil {
		require.NoError(t, cg.AddProc(uint64(cmd.Process.Pid)))
	}
	return cmd, waited
}

// TestKillCgroup verifies the kill used for cgroup v1 and, on kernels without
// cgroup.kill, cgroup v2, against a frozen cgroup.
func TestKillCgroup(t *testing.T) {
	cg, group := testCgroup(t)
	_, waited := startInCgroup(t, cg)
	require.NoError(t, cg.Freeze())

	ops := cgroup2Ops(cg, filepath.Join(cgroup2Mountpoint, group))
	require.NoError(t, killCgroup(context.Background(), ops))

	assert.Error(t, <-waited, "the process should have been killed")
	procs, err := ops.procs()
	assert.NoError(t, err)
	assert.Empty(t, procs)
	frozen, err := ops.frozen()
	assert.NoError(t, err)
	assert.False(t, frozen, "the cgroup should have been thawed")
}

// TestKillCgroupProcsMembersOnly verifies that a listed process that is no
// longer in the cgroup, as happens when its PID is reused, is not signalled.
func TestKillCgroupProcsMembersOnly(t *testing.T) {
	cg, group := testCgroup(t)
	member, memberWaited := startInCgroup(t, cg)
	outsider, outsiderWaited := startInCgroup(t, nil)

	ops := cgroup2Ops(cg, filepath.Join(cgroup2Mountpoint, group))
	procs := []int{member.Process.Pid, outsider.Process.Pid}
	require.NoError(t, killCgroupProcs(context.Background(), ops, procs, false))

	assert.Error(t, <-memberWaited, "the member should have been killed")
	select {
	case err := <-outsiderWaited:
		t.Fatalf("the process outside of the cgroup should not have been killed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestKillFrozenCgroupProcsNotFrozen(t *testing.T) {
	cmd, waited := startInCgroup(t, nil)
	assert.Error(t, killFrozenCgroupProcs(context.Background(), []int{cmd.Process.Pid}, false))
	select {
	case err := <-waited:
		t.Fatalf("the process should not have been killed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCgroup1ProcsSubsystem(t *testing.T) {
	pids := cgroup1.NewPids("/sys/fs/cgroup")
	freezer := cgroup1.NewFreezer("/sys/fs/cgroup")
	assert.Equal(t, cgroup1.Freezer, cgroup1ProcsSubsystem([]cgroup1.Subsystem{pids, freezer}).Name())
	// Without the freezer, nor the devices subsystem as in a user namespace.
	assert.Equal(t, cgroup1.Pids, cgroup1ProcsSubsystem([]cgroup1.Subsystem{pids}).Name())
}

func TestIsRootCgroup(t *testing.T) {
	for _, path := range []string{"", "/", ".", "//", "/.."} {
		assert.True(t, isRootCgroup(path), path)
	}
	for _, path := range []string{"/leakrepro/abc", "leakrepro", "/system.slice/containerd-abc.scope"} {
		assert.False(t, isRootCgroup(path), path)
	}
	// systemd notation always names a scope below the slice, even the root one.
	assert.False(t, isRootCgroup(containerCgroupPath("-.slice:cri-containerd:abc", true)))
}
