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
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	kernel "github.com/containerd/containerd/v2/pkg/kernelversion"
	"github.com/containerd/continuity/testutil"
	"github.com/stretchr/testify/require"
)

func BenchmarkBatchRunGetUsernsFD_Concurrent1(b *testing.B) {
	for range b.N {
		benchmarkBatchRunGetUsernsFD(1)
	}
}

func BenchmarkBatchRunGetUsernsFD_Concurrent10(b *testing.B) {
	for range b.N {
		benchmarkBatchRunGetUsernsFD(10)
	}
}

func benchmarkBatchRunGetUsernsFD(n int) {
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			fd, err := getUsernsFD(testUIDMaps, testGIDMaps)
			if err != nil {
				panic(err)
			}
			fd.Close()
		}()
	}
	wg.Wait()
}

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

func TestIdmappedMount(t *testing.T) {
	testutil.RequiresRoot(t)

	k512 := kernel.KernelVersion{Kernel: 5, Major: 12}
	ok, err := kernel.GreaterEqualThan(k512)
	require.NoError(t, err)
	if !ok {
		t.Skip("GetUsernsFD requires kernel >= 5.12")
	}

	t.Run("GetUsernsFD", testGetUsernsFD)

	t.Run("IDMapMount", testIDMapMount)

	t.Run("IDMapMountWithAttrs", testIDMapMountWithAttrs)
}

func testGetUsernsFD(t *testing.T) {
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

func testIDMapMount(t *testing.T) {
	usernsFD, err := getUsernsFD(testUIDMaps, testGIDMaps)
	require.NoError(t, err)
	defer usernsFD.Close()

	srcDir, checkFunc := initIDMappedChecker(t, testUIDMaps, testGIDMaps, true)
	destDir := t.TempDir()
	defer func() {
		require.NoError(t, UnmountAll(destDir, 0))
	}()

	err = IDMapMount(srcDir, destDir, int(usernsFD.Fd()))
	require.NoError(t, err)
	checkFunc(destDir)
}

func testIDMapMountWithAttrs(t *testing.T) {
	usernsFD, err := getUsernsFD(testUIDMaps, testGIDMaps)
	require.NoError(t, err)
	defer usernsFD.Close()

	type testCase struct {
		name      string
		srcDir    string
		setAttr   uint64
		checkFunc func(destDir string)
	}

	cases := make([]testCase, 0)
	srcDir, checkFunc := initIDMappedChecker(t, testUIDMaps, testGIDMaps, true)
	cases = append(cases, testCase{
		name:      "Writable idmapped mount",
		srcDir:    srcDir,
		setAttr:   0,
		checkFunc: checkFunc,
	})

	srcDir, checkFunc = initIDMappedChecker(t, testUIDMaps, testGIDMaps, false)
	cases = append(cases, testCase{
		name:      "Readonly idmapped mount",
		srcDir:    srcDir,
		setAttr:   unix.MOUNT_ATTR_RDONLY,
		checkFunc: checkFunc,
	})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			destDir := t.TempDir()
			defer func() {
				require.NoError(t, UnmountAll(destDir, 0))
			}()
			err := IDMapMountWithAttrs(tc.srcDir, destDir, int(usernsFD.Fd()), tc.setAttr, 0)
			require.NoError(t, err)
			tc.checkFunc(destDir)
		})
	}
}

func initIDMappedChecker(t *testing.T, uidMaps, gidMaps []syscall.SysProcIDMap, expectWritable bool) (_srcDir string, _verifyFunc func(destDir string)) {
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

	writableDir := filepath.Join(srcDir, "write-test")
	require.NoError(t, os.Mkdir(writableDir, os.ModePerm))
	require.NoError(t, os.Chmod(writableDir, os.ModePerm))
	require.NoError(t, os.Chown(writableDir, uidMaps[0].ContainerID, gidMaps[0].ContainerID))

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

		wf, err := os.Create(filepath.Join(destDir, "write-test", "1"))
		if err == nil {
			defer wf.Close()
		}
		if expectWritable {
			require.NoError(t, err, "create write-test file")
		} else {
			require.Error(t, err)
			pathErr, isPathErr := err.(*fs.PathError)
			require.True(t, isPathErr, "Expecting path error")
			require.Equal(t, unix.EROFS, pathErr.Err, "Expecting read-only filesystem error")
		}
	}
}
