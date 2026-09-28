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
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/containerd/containerd/v2/internal/userns"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/continuity/sysx"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
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
			if revision != capRevision1 {
				binary.LittleEndian.PutUint32(caps[12:], 0x10)
				binary.LittleEndian.PutUint32(caps[16:], 0x20)
			}
			original := append([]byte(nil), caps...)
			mapped, err := remapFileCapabilities(caps, testCapabilityMap())
			require.NoError(t, err)
			expected := uint32(100000)
			if revision == capRevision3 {
				expected += 42
			}
			require.Equal(t, expected, binary.LittleEndian.Uint32(mapped[20:]))
			require.Equal(t, uint32(capRevision3|capEffective), binary.LittleEndian.Uint32(mapped))
			require.Equal(t, original, caps)
			end := len(caps)
			if end > 20 {
				end = 20
			}
			require.Equal(t, caps[4:end], mapped[4:end])
		})
	}
	t.Run("no-effective-flag", func(t *testing.T) {
		caps := testCapability(capRevision2, 0)
		binary.LittleEndian.PutUint32(caps, capRevision2)
		mapped, err := remapFileCapabilities(caps, testCapabilityMap())
		require.NoError(t, err)
		require.Equal(t, uint32(capRevision3), binary.LittleEndian.Uint32(mapped))
	})
	t.Run("unmapped-root", func(t *testing.T) {
		_, err := remapFileCapabilities(testCapability(capRevision3, 65536), testCapabilityMap())
		require.Error(t, err)
	})
	for _, caps := range [][]byte{nil, {1}, make([]byte, 4), testCapability(capRevision2, 0)[:19], append(testCapability(capRevision2, 0), 0)} {
		_, err := remapFileCapabilities(caps, testCapabilityMap())
		require.Error(t, err)
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
	root := t.TempDir()
	path := filepath.Join(root, "binary")
	require.NoError(t, os.WriteFile(path, []byte("executable"), 0755))
	require.NoError(t, os.Chmod(path, 0755|os.ModeSetuid|os.ModeSetgid))
	setTestCapability(t, path)
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.NoError(t, chown(root, testCapabilityMap())(path, info, nil))
	caps, err := sysx.LGetxattr(path, fileCapabilityXattr)
	require.NoError(t, err)
	require.Equal(t, testCapability(capRevision3, 100000), caps)
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
	require.NoError(t, chown(root, testCapabilityMap())(link, info, nil))
	caps, err = sysx.LGetxattr(path, fileCapabilityXattr)
	require.NoError(t, err)
	require.Equal(t, testCapability(capRevision3, 100000), caps)
	plain := filepath.Join(root, "plain")
	require.NoError(t, os.WriteFile(plain, nil, 0644))
	info, err = os.Lstat(plain)
	require.NoError(t, err)
	require.NoError(t, chown(root, testCapabilityMap())(plain, info, nil))
	_, err = sysx.LGetxattr(plain, fileCapabilityXattr)
	require.ErrorIs(t, err, unix.ENODATA)
}

// Check actual exec capability acquisition, not just the xattr bytes.
func TestRemappedFileCapabilitiesExec(t *testing.T) {
	if os.Getenv("CONTAINERD_FILECAP_HELPER") == "1" {
		if target := os.Getenv("CONTAINERD_FILECAP_EXEC_TARGET"); target != "" {
			runtime.LockOSThread()
			require.NoError(t, unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0))
			env := []string{}
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "CONTAINERD_FILECAP_EXEC_TARGET=") {
					env = append(env, entry)
				}
			}
			require.NoError(t, syscall.Exec(target, []string{target, "-test.run=^TestRemappedFileCapabilitiesExec$"}, env))
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
	src, err := os.Open(self)
	require.NoError(t, err)
	defer src.Close()
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0755)
	require.NoError(t, err)
	_, err = io.Copy(dst, src)
	require.NoError(t, err)
	require.NoError(t, dst.Close())
	setTestCapability(t, path)
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.NoError(t, chown(root, testCapabilityMap())(path, info, nil))
	for _, tc := range []struct {
		name    string
		hostUID int
		want    string
	}{
		{"mapped-namespace", 100000, "0000000000001000"},
		{"unrelated-namespace", 300000, "0000000000000000"},
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
			out, err := cmd.CombinedOutput()
			if errors.Is(err, syscall.EPERM) {
				t.Skipf("user namespace creation unavailable: %v", err)
			}
			require.NoError(t, err, string(out))
			require.Contains(t, string(out), "CapEff:\t"+tc.want)
			require.Contains(t, string(out), "Uid:\t65534\t65534\t65534\t65534")
		})
	}
	t.Run("no-new-privileges", func(t *testing.T) {
		// Supply NET_ADMIN before exec, as the runtime does. A plain launcher
		// sets no_new_privs before executing the file-capability binary.
		launcher := filepath.Join(root, "launcher")
		_, err := src.Seek(0, io.SeekStart)
		require.NoError(t, err)
		f, err := os.OpenFile(launcher, os.O_CREATE|os.O_WRONLY, 0755)
		require.NoError(t, err)
		_, err = io.Copy(f, src)
		require.NoError(t, err)
		require.NoError(t, f.Close())
		cmd := exec.Command(launcher, "-test.run=^TestRemappedFileCapabilitiesExec$")
		cmd.Env = append(os.Environ(), "CONTAINERD_FILECAP_HELPER=1", "CONTAINERD_FILECAP_EXEC_TARGET="+path)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET,
			UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: 100000, Size: 65536}},
			GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: 200000, Size: 65536}},
			Credential:  &syscall.Credential{Uid: 65534, Gid: 65534},
			AmbientCaps: []uintptr{unix.CAP_NET_ADMIN},
		}
		out, err := cmd.CombinedOutput()
		if errors.Is(err, syscall.EPERM) {
			t.Skipf("user namespace creation unavailable: %v", err)
		}
		require.NoError(t, err, string(out))
		require.Contains(t, string(out), "CapEff:\t0000000000001000")
		require.Contains(t, string(out), "CapAmb:\t0000000000000000")
		require.Contains(t, string(out), "NoNewPrivs:\t1")
	})
}
