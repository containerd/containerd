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

package mount

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	kernel "github.com/containerd/containerd/v2/contrib/seccomp/kernelversion"
	"github.com/containerd/continuity/testutil"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

var (
	testUIDMaps = []syscall.SysProcIDMap{
		{ContainerID: 1000, HostID: 0, Size: 100},
		{ContainerID: 5000, HostID: 2000, Size: 100},
		{ContainerID: 10000, HostID: 3000, Size: 100},
	}

	testGIDMaps = []syscall.SysProcIDMap{
		{ContainerID: 1000, HostID: 0, Size: 100},
		{ContainerID: 5000, HostID: 2000, Size: 100},
		{ContainerID: 10000, HostID: 3000, Size: 100},
	}
)

func TestGetUsernsFD(t *testing.T) {
	testutil.RequiresRoot(t)

	k512 := kernel.KernelVersion{Kernel: 5, Major: 12}
	ok, err := kernel.GreaterEqualThan(k512)
	require.NoError(t, err)
	if !ok {
		t.Skip("GetUsernsFD requires kernel >= 5.12")
	}

	t.Run("basic", testGetUsernsFDBasic)

	t.Run("when kill child process before write u[g]id maps", testGetUsernsFDKillChildWhenWriteUGIDMaps)

	t.Run("when kill child process after open u[g]id_map file", testGetUsernsFDKillChildAfterOpenUGIDMapFiles)

}

func testGetUsernsFDBasic(t *testing.T) {
	for idx, tc := range []struct {
		uidMaps string
		gidMaps string
		hasErr  bool
	}{
		{
			uidMaps: "0:1000:100",
			gidMaps: "0:1000:100",
			hasErr:  false,
		},
		{
			uidMaps: "100:1000:100",
			gidMaps: "0:-1:100",
			hasErr:  true,
		},
		{
			uidMaps: "100:1000:100",
			gidMaps: "-1:1000:100",
			hasErr:  true,
		},
		{
			uidMaps: "100:1000:100",
			gidMaps: "0:1000:-1",
			hasErr:  true,
		},
	} {
		t.Run(fmt.Sprintf("#%v", idx), func(t *testing.T) {
			_, err := GetUsernsFD(tc.uidMaps, tc.gidMaps)
			if tc.hasErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func testGetUsernsFDKillChildWhenWriteUGIDMaps(t *testing.T) {
	hookFunc := func(reap bool) func(uintptr, *os.File) {
		return func(_pid uintptr, pidFD *os.File) {
			err := unix.PidfdSendSignal(int(pidFD.Fd()), unix.SIGKILL, nil, 0)
			require.NoError(t, err)

			if reap {
				pidfdWaitid(pidFD)
			}
		}
	}

	for _, tcReap := range []bool{true, false} {
		t.Run(fmt.Sprintf("#reap=%v", tcReap), func(t *testing.T) {
			updateTestHookKillForGetUsernsFD(t, nil, hookFunc(tcReap))

			usernsFD, err := getUsernsFD(testUIDMaps, testGIDMaps)
			require.NoError(t, err)
			defer usernsFD.Close()

			srcDir, checkFunc := initIDMappedChecker(t, testUIDMaps, testGIDMaps)
			destDir := t.TempDir()
			defer func() {
				require.NoError(t, UnmountAll(destDir, 0))
			}()

			err = IDMapMount(srcDir, destDir, int(usernsFD.Fd()))
			usernsFD.Close()
			require.NoError(t, err)
			checkFunc(destDir)
		})
	}

}

func testGetUsernsFDKillChildAfterOpenUGIDMapFiles(t *testing.T) {
	hookFunc := func(reap bool) func(uintptr, *os.File) {
		return func(_pid uintptr, pidFD *os.File) {
			err := unix.PidfdSendSignal(int(pidFD.Fd()), unix.SIGKILL, nil, 0)
			require.NoError(t, err)

			if reap {
				pidfdWaitid(pidFD)
			}
		}
	}

	for _, tc := range []struct {
		reap     bool
		expected error
	}{
		{
			reap:     false,
			expected: nil,
		},
		{
			reap:     true,
			expected: syscall.ESRCH,
		},
	} {
		t.Run(fmt.Sprintf("#reap=%v", tc.reap), func(t *testing.T) {
			updateTestHookKillForGetUsernsFD(t, hookFunc(tc.reap), nil)

			usernsFD, err := getUsernsFD(testUIDMaps, testGIDMaps)
			if tc.expected != nil {
				require.Error(t, tc.expected, err)
				return
			}

			require.NoError(t, err)
			defer usernsFD.Close()

			srcDir, checkFunc := initIDMappedChecker(t, testUIDMaps, testGIDMaps)
			destDir := t.TempDir()
			defer func() {
				require.NoError(t, UnmountAll(destDir, 0))
			}()

			err = IDMapMount(srcDir, destDir, int(usernsFD.Fd()))
			usernsFD.Close()
			require.NoError(t, err)
			checkFunc(destDir)
		})
	}
}

func updateTestHookKillForGetUsernsFD(t *testing.T, newBeforeFunc, newAfterFunc func(uintptr, *os.File)) {
	testHookLock.Lock()

	oldBefore := testHookKillChildBeforePidfdSendSignal
	oldAfter := testHookKillChildAfterPidfdSendSignal
	t.Cleanup(func() {
		testHookKillChildBeforePidfdSendSignal = oldBefore
		testHookKillChildAfterPidfdSendSignal = oldAfter
		testHookLock.Unlock()
	})
	if newBeforeFunc != nil {
		testHookKillChildBeforePidfdSendSignal = newBeforeFunc
	}
	if newAfterFunc != nil {
		testHookKillChildAfterPidfdSendSignal = newAfterFunc
	}
}

func initIDMappedChecker(t *testing.T, uidMaps, gidMaps []syscall.SysProcIDMap) (_srcDir string, _verifyFunc func(destDir string)) {
	testutil.RequiresRoot(t)

	srcDir := t.TempDir()

	require.Equal(t, len(uidMaps), len(gidMaps))
	for idx := range uidMaps {
		file := filepath.Join(srcDir, fmt.Sprintf("%v", idx))

		f, err := os.Create(file)
		require.NoError(t, err, fmt.Sprintf("create file %s", file))
		defer f.Close()

		uid, gid := uidMaps[idx].ContainerID, gidMaps[idx].ContainerID
		err = f.Chown(uid, gid)
		require.NoError(t, err, fmt.Sprintf("chown %v:%v for file %s", uid, gid, file))
	}

	return srcDir, func(destDir string) {
		for idx := range uidMaps {
			file := filepath.Join(destDir, fmt.Sprintf("%v", idx))

			f, err := os.Open(file)
			require.NoError(t, err, fmt.Sprintf("open file %s", file))
			defer f.Close()

			stat, err := f.Stat()
			require.NoError(t, err, fmt.Sprintf("stat file %s", file))

			sysStat := stat.Sys().(*syscall.Stat_t)

			uid, gid := uidMaps[idx].HostID, gidMaps[idx].HostID
			require.Equal(t, uint32(uid), sysStat.Uid, fmt.Sprintf("check file %s uid", file))
			require.Equal(t, uint32(gid), sysStat.Gid, fmt.Sprintf("check file %s gid", file))
			t.Logf("IDMapped File %s uid=%v, gid=%v", file, uid, gid)
		}
	}
}
