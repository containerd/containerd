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
)

func TestUnshareAfterEnterUserns(t *testing.T) {
	testutil.RequiresRoot(t)

	k510 := kernel.KernelVersion{Kernel: 5, Major: 10}
	ok, err := kernel.GreaterEqualThan(k510)
	require.NoError(t, err)
	if !ok {
		t.Skip("Requires kernel >= 5.10")
	}

	err = UnshareAfterEnterUserns("0:1000:1", "0:1000:1", syscall.CLONE_NEWUSER|syscall.CLONE_NEWIPC, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "unshare flags should not include user namespace")

	t.Run("should work", testUnshareAfterEnterUsernsShouldWork)
	t.Run("killpid", testUnshareAfterEnterUsernsKillPid)
	t.Run("invalid unshare flags", testUnshareAfterEnterUsernsInvalidFlags)
}

func testUnshareAfterEnterUsernsShouldWork(t *testing.T) {
	t.Parallel()

	currentNetNs, err := getNamespaceInode(os.Getpid(), "net")
	require.NoError(t, err)

	currentUserNs, err := getNamespaceInode(os.Getpid(), "user")
	require.NoError(t, err)

	currentIpcNs, err := getNamespaceInode(os.Getpid(), "ipc")
	require.NoError(t, err)

	currentPidNs, err := getNamespaceInode(os.Getpid(), "pid")
	require.NoError(t, err)

	uerr := UnshareAfterEnterUserns("0:1000:10", "0:1000:10", syscall.CLONE_NEWIPC|syscall.CLONE_NEWNET, func(pid int) error {
		netNs, err := getNamespaceInode(pid, "net")
		require.NoError(t, err)
		require.NotEqual(t, currentNetNs, netNs)

		userNs, err := getNamespaceInode(pid, "user")
		require.NoError(t, err)
		require.NotEqual(t, currentUserNs, userNs)

		ipcNs, err := getNamespaceInode(pid, "ipc")
		require.NoError(t, err)
		require.NotEqual(t, currentIpcNs, ipcNs)

		pidNs, err := getNamespaceInode(pid, "pid")
		require.NoError(t, err)
		require.Equal(t, currentPidNs, pidNs)

		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/uid_map", pid))
		require.NoError(t, err)
		require.Equal(t, "         0       1000         10\n", string(data))

		data, err = os.ReadFile(fmt.Sprintf("/proc/%d/gid_map", pid))
		require.NoError(t, err)
		require.Equal(t, "         0       1000         10\n", string(data))

		data, err = os.ReadFile(fmt.Sprintf("/proc/%d/setgroups", pid))
		require.NoError(t, err)
		require.Equal(t, "allow\n", string(data))
		return nil
	})
	require.NoError(t, uerr)
}

func testUnshareAfterEnterUsernsKillPid(t *testing.T) {
	t.Parallel()

	uerr := UnshareAfterEnterUserns("0:1000:1", "0:1000:1", syscall.CLONE_NEWIPC|syscall.CLONE_NEWNET, func(pid int) error {
		proc, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("failed to find process: %w", err)
		}

		if err := proc.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}

		proc.Wait()

		_, err = os.OpenFile(fmt.Sprintf("/proc/%d/ns/net", pid), os.O_RDONLY, 0600)
		require.Error(t, err)
		require.ErrorIs(t, err, os.ErrNotExist)
		return err
	})
	require.Error(t, uerr)
	require.ErrorIs(t, uerr, os.ErrNotExist)

	uerr = UnshareAfterEnterUserns("0:1000:1", "0:1000:1", syscall.CLONE_NEWIPC|syscall.CLONE_NEWNET, func(pid int) error {
		proc, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("failed to find process: %w", err)
		}

		if err := proc.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}

		proc.Wait()

		return nil
	})
	require.Error(t, uerr)
	require.ErrorContains(t, uerr, "failed to ensure child process is alive: no such process")
}

func testUnshareAfterEnterUsernsInvalidFlags(t *testing.T) {
	t.Parallel()

	uerr := UnshareAfterEnterUserns("0:1000:1", "0:1000:1", syscall.CLONE_IO, nil)
	require.Error(t, uerr)
	require.ErrorContains(t, uerr, "fork/exec /proc/self/exe: invalid argument")
}

func getNamespaceInode(pid int, typ string) (uint64, error) {
	info, err := os.Stat(fmt.Sprintf("/proc/%d/ns/%s", pid, typ))
	if err != nil {
		return 0, err
	}

	return info.Sys().(*syscall.Stat_t).Ino, nil
}
