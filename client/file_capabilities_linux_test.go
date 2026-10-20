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

package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/containerd/containerd/v2/internal/userns"
	"github.com/containerd/containerd/v2/pkg/kernelversion"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/continuity/sysx"
	"github.com/containerd/log"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// Raw xattr fixtures keep filesystem tests independent of the encoder.
const (
	capRevision1 = 0x01000000
	capRevision2 = 0x02000000
	capRevision3 = 0x03000000
	capEffective = 1
	capSize1     = 12
	capSize2     = 20
	capSize3     = 24
)

func testCapability(revision uint32, root uint32) []byte {
	size := map[uint32]int{capRevision1: capSize1, capRevision2: capSize2, capRevision3: capSize3}[revision]
	caps := make([]byte, size)
	binary.LittleEndian.PutUint32(caps, revision|capEffective)
	binary.LittleEndian.PutUint32(caps[4:], 1<<unix.CAP_NET_ADMIN)
	if revision == capRevision3 {
		binary.LittleEndian.PutUint32(caps[20:], root)
	}
	return caps
}

func testCapabilityMap() userns.IDMap {
	return userns.IDMap{
		UidMap: []specs.LinuxIDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
		GidMap: []specs.LinuxIDMapping{{ContainerID: 0, HostID: 200000, Size: 65536}},
	}
}

func TestRemapFileCapabilities(t *testing.T) {
	for _, revision := range []uint32{capRevision1, capRevision2, capRevision3} {
		t.Run(fmt.Sprintf("revision-%x", revision), func(t *testing.T) {
			caps := testCapability(revision, 42)
			mapped, err := remapFileCapabilities(caps, testCapabilityMap())
			require.NoError(t, err)
			expected := uint32(100000)
			if revision == capRevision3 {
				expected += 42
			}
			require.Equal(t, testCapability(capRevision3, expected), mapped)
		})
	}

	t.Run("unmapped-root", func(t *testing.T) {
		caps, err := remapFileCapabilities(testCapability(capRevision3, 65536), testCapabilityMap())
		require.NoError(t, err)
		require.Nil(t, caps)
	})
	for name, mapping := range map[string][]specs.LinuxIDMapping{
		"empty-map":            {},
		"zero-size":            {{Size: 0}},
		"source-overflow":      {{ContainerID: 1, Size: userns.InvalidID}},
		"destination-overflow": {{HostID: userns.InvalidID, Size: 2}},
		"mapped-root-overflow": {{ContainerID: 99999, HostID: userns.InvalidID - 1, Size: 65536}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := remapFileCapabilities(testCapability(capRevision3, 100000), userns.IDMap{UidMap: mapping})
			require.Error(t, err)
		})
	}
	_, err := remapFileCapabilities([]byte{1}, testCapabilityMap())
	require.Error(t, err)
	_, err = remapFileCapabilities(testCapability(capRevision3, userns.InvalidID), testCapabilityMap())
	require.Error(t, err)
	// An implicit root in revisions 1/2 must not be silently discarded.
	for _, revision := range []uint32{capRevision1, capRevision2} {
		mapping := testCapabilityMap()
		mapping.UidMap[0].ContainerID = 1
		_, err := remapFileCapabilities(testCapability(revision, 0), mapping)
		require.ErrorIs(t, err, userns.ErrUnmappedID)
	}
}

func setTestCapability(t *testing.T, path string) {
	t.Helper()
	err := sysx.LSetxattr(path, fileCapabilityXattr, testCapability(capRevision2, 0), 0)
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EPERM) {
		t.Skipf("file capabilities unavailable: %v", err)
	}
	require.NoError(t, err)
}

func TestChownPreservesFileCapabilities(t *testing.T) {
	testutil.RequiresRoot(t)
	supported, err := kernelversion.GreaterEqualThan(kernelversion.KernelVersion{Kernel: 4, Major: 14})
	require.NoError(t, err)
	root := t.TempDir()
	path := filepath.Join(root, "binary")
	require.NoError(t, os.WriteFile(path, []byte("executable"), 0755))
	require.NoError(t, os.Chmod(path, 0755|os.ModeSetuid|os.ModeSetgid))
	setTestCapability(t, path)
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.NoError(t, chown(context.Background(), root, testCapabilityMap())(path, info, nil))
	checkCaps := func() {
		t.Helper()
		caps, err := sysx.LGetxattr(path, fileCapabilityXattr)
		if supported {
			require.NoError(t, err)
			require.Equal(t, testCapability(capRevision3, 100000), caps)
		} else {
			require.ErrorIs(t, err, unix.ENODATA)
		}
	}
	checkCaps()
	info, err = os.Lstat(path)
	require.NoError(t, err)
	require.Equal(t, uint32(100000), info.Sys().(*syscall.Stat_t).Uid)
	require.Equal(t, uint32(200000), info.Sys().(*syscall.Stat_t).Gid)
	require.Equal(t, os.ModeSetuid|os.ModeSetgid, info.Mode()&(os.ModeSetuid|os.ModeSetgid))
	// A symlink must not follow or remap its target again.
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(path, link))
	info, err = os.Lstat(link)
	require.NoError(t, err)
	require.NoError(t, chown(context.Background(), root, testCapabilityMap())(link, info, nil))
	checkCaps()
	plain := filepath.Join(root, "plain")
	require.NoError(t, os.WriteFile(plain, nil, 0644))
	info, err = os.Lstat(plain)
	require.NoError(t, err)
	require.NoError(t, chown(context.Background(), root, testCapabilityMap())(plain, info, nil))
	_, err = sysx.LGetxattr(plain, fileCapabilityXattr)
	require.ErrorIs(t, err, unix.ENODATA)
}

// Check actual exec capability acquisition, not just the xattr bytes.
func TestRemappedFileCapabilitiesExec(t *testing.T) {
	if os.Getenv("CONTAINERD_FILECAP_HELPER") == "1" {
		if target := os.Getenv("CONTAINERD_FILECAP_EXEC_TARGET"); target != "" {
			runtime.LockOSThread()
			require.NoError(t, unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0))
			require.NoError(t, os.Unsetenv("CONTAINERD_FILECAP_EXEC_TARGET"))
			require.NoError(t, syscall.Exec(target, []string{target, "-test.run=^TestRemappedFileCapabilitiesExec$"}, os.Environ()))
		}
		data, err := os.ReadFile("/proc/self/status")
		require.NoError(t, err)
		fmt.Print(string(data))
		return
	}
	testutil.RequiresRoot(t)
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0755))
	// The testing package creates a parent directory with mode 0700.
	require.NoError(t, os.Chmod(filepath.Dir(root), 0755))
	path := filepath.Join(root, "probe")
	self, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, fs.CopyFile(path, self))
	require.NoError(t, os.Chmod(path, 0755))
	setTestCapability(t, path)
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.NoError(t, chown(context.Background(), root, testCapabilityMap())(path, info, nil))
	supported, err := kernelversion.GreaterEqualThan(kernelversion.KernelVersion{Kernel: 4, Major: 14})
	require.NoError(t, err)
	var mappedCaps uint64
	if supported {
		mappedCaps = 1 << unix.CAP_NET_ADMIN
	}
	for _, tc := range []struct {
		name       string
		hostUID    int
		noNewPrivs bool
		wantCaps   uint64
	}{
		{"mapped-namespace", 100000, false, mappedCaps},
		{"unrelated-namespace", 300000, false, 0},
		{"no-new-privileges", 100000, true, 1 << unix.CAP_NET_ADMIN},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(path, "-test.run=^TestRemappedFileCapabilitiesExec$")
			cmd.Env = append(os.Environ(), "CONTAINERD_FILECAP_HELPER=1")
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET,
				UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: tc.hostUID, Size: 65536}},
				GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: 200000, Size: 65536}},
				Credential:  &syscall.Credential{Uid: 65534, Gid: 65534},
			}
			if tc.noNewPrivs {
				ambientSupported, err := kernelversion.GreaterEqualThan(kernelversion.KernelVersion{Kernel: 4, Major: 3})
				require.NoError(t, err)
				if !ambientSupported {
					t.Skip("ambient capabilities require Linux 4.3")
				}
				// A plain launcher sets no_new_privs before executing the capability binary.
				cmd.Path = filepath.Join(root, "launcher")
				require.NoError(t, fs.CopyFile(cmd.Path, self))
				require.NoError(t, os.Chmod(cmd.Path, 0755))
				cmd.Env = append(cmd.Env, "CONTAINERD_FILECAP_EXEC_TARGET="+path)
				cmd.SysProcAttr.AmbientCaps = []uintptr{unix.CAP_NET_ADMIN}
			}
			out, err := cmd.CombinedOutput()
			if errors.Is(err, syscall.EPERM) {
				t.Skipf("user namespace creation unavailable: %v", err)
			}
			require.NoError(t, err, string(out))
			require.Contains(t, string(out), fmt.Sprintf("CapEff:\t%016x", tc.wantCaps))
			require.Contains(t, string(out), "Uid:\t65534\t65534\t65534\t65534")
			if tc.noNewPrivs {
				var ambientCaps uint64
				if !supported {
					// Without file capabilities, exec retains the supplied ambient set.
					ambientCaps = tc.wantCaps
				}
				require.Contains(t, string(out), fmt.Sprintf("CapAmb:\t%016x", ambientCaps))
				require.Contains(t, string(out), "NoNewPrivs:\t1")
			}
		})
	}
}

func TestChownDropsUnmappedFileCapabilities(t *testing.T) {
	testutil.RequiresRoot(t)
	root := t.TempDir()
	path := filepath.Join(root, "binary")
	require.NoError(t, os.WriteFile(path, []byte("executable"), 0755))
	setTestCapability(t, path)
	caps := testCapability(capRevision3, 100000)
	require.NoError(t, sysx.LSetxattr(path, fileCapabilityXattr, caps, 0))
	before, err := sysx.LGetxattr(path, fileCapabilityXattr)
	require.NoError(t, err)
	require.Equal(t, caps, before)

	require.NoError(t, filepath.Walk(root, chown(context.Background(), root, testCapabilityMap())))
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.Equal(t, uint32(100000), info.Sys().(*syscall.Stat_t).Uid)
	require.Equal(t, uint32(200000), info.Sys().(*syscall.Stat_t).Gid)
	_, err = sysx.LGetxattr(path, fileCapabilityXattr)
	require.ErrorIs(t, err, unix.ENODATA)
}

func TestFileCapabilityRemapWarnings(t *testing.T) {
	testutil.RequiresRoot(t)
	var output bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&output)
	ctx := log.WithLogger(context.Background(), logrus.NewEntry(logger))
	supported, err := supportsNamespacedFileCapabilities()
	require.NoError(t, err)
	for remap := 0; remap < 2; remap++ {
		t.Run(fmt.Sprint(remap), func(t *testing.T) {
			output.Reset()
			root := t.TempDir()
			walk := chown(ctx, root, testCapabilityMap())
			for _, name := range []string{"plain", "first", "second", "mapped"} {
				path := filepath.Join(root, name)
				require.NoError(t, os.WriteFile(path, nil, 0755))
				if name != "plain" {
					setTestCapability(t, path)
				}
				if name == "first" || name == "second" {
					require.NoError(t, sysx.LSetxattr(path, fileCapabilityXattr, testCapability(capRevision3, 100000), 0))
				}
				info, err := os.Lstat(path)
				require.NoError(t, err)
				require.NoError(t, walk(path, info, nil))
				caps, err := sysx.LGetxattr(path, fileCapabilityXattr)
				if supported && name == "mapped" {
					require.NoError(t, err)
					require.Equal(t, testCapability(capRevision3, 100000), caps)
				} else {
					require.ErrorIs(t, err, unix.ENODATA)
				}
				if supported && name == "plain" {
					require.Empty(t, output.String())
				}
			}
			require.Equal(t, 1, bytes.Count(output.Bytes(), []byte("level=warning")))
			if supported {
				require.Contains(t, output.String(), "outside the UID mapping")
			} else {
				require.Contains(t, output.String(), "require Linux 4.14")
			}
		})
	}
}

func TestFileCapabilityIOErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	supported, err := supportsNamespacedFileCapabilities()
	require.NoError(t, err)
	if supported {
		_, err = remappedFileCapabilities(missing, testCapabilityMap(), func(reason string) {
			t.Fatalf("unexpected warning: %s", reason)
		})
		require.ErrorIs(t, err, unix.ENOENT)
	}
	require.ErrorIs(t, restoreFileCapabilities(missing, testCapability(capRevision3, 100000)), unix.ENOENT)
}
