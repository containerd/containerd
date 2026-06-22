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

package sys

import (
	"fmt"
	"os"
	"syscall"
	"testing"

	kernel "github.com/containerd/containerd/v2/pkg/kernelversion"
	"github.com/containerd/continuity/testutil"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestGetUsernsForNamespace(t *testing.T) {
	testutil.RequiresRoot(t)

	t.Parallel()

	k409 := kernel.KernelVersion{Kernel: 4, Major: 9}
	ok, err := kernel.GreaterEqualThan(k409)
	require.NoError(t, err)
	if !ok {
		t.Skip("Requires kernel >= 4.9")
	}

	tmpDir := t.TempDir()

	f, err := os.CreateTemp(tmpDir, "netns")
	require.NoError(t, err)

	netnsPath := f.Name()
	f.Close()

	defer testutil.Unmount(t, netnsPath)

	currentUsernsIno, err := getNamespaceInode(os.Getpid(), "user")
	require.NoError(t, err)

	usernsIno := uint64(0)
	uerr := UnshareAfterEnterUserns("0:1000:10", "0:1000:10", syscall.CLONE_NEWNET, func(pid int) error {
		err := unix.Mount(
			fmt.Sprintf("/proc/%d/ns/net", pid),
			netnsPath,
			"",
			unix.MS_BIND|unix.MS_RDONLY,
			"",
		)
		if err != nil {
			return err
		}

		usernsIno, err = getNamespaceInode(pid, "user")
		if err != nil {
			return err
		}
		return nil
	})
	require.NoError(t, uerr)

	require.NotEqual(t, currentUsernsIno, usernsIno)
	t.Logf("Current user namespace [%d], new user namespace [%d]", currentUsernsIno, usernsIno)

	netnsFd, err := os.Open(netnsPath)
	require.NoError(t, err)
	defer netnsFd.Close()

	usernsFd, err := GetUsernsForNamespace(netnsFd.Fd())
	require.NoError(t, err)
	defer usernsFd.Close()

	usernsInoFromNetnsFd := getInode(t, usernsFd)

	t.Logf("Fetch netns namespace %s' user namespace owner %d", netnsPath, usernsInoFromNetnsFd)
	require.Equal(t, usernsIno, usernsInoFromNetnsFd)

	parentUsernsFd, err := GetUsernsForNamespace(usernsFd.Fd())
	require.NoError(t, err)
	defer parentUsernsFd.Close()

	parentUsernsIno := getInode(t, parentUsernsFd)
	t.Logf("User namespace %d's parent %d", usernsInoFromNetnsFd, parentUsernsIno)
	require.Equal(t, currentUsernsIno, parentUsernsIno)
}

func getInode(t *testing.T, f *os.File) uint64 {
	info, err := f.Stat()
	require.NoError(t, err)
	return info.Sys().(*syscall.Stat_t).Ino
}
