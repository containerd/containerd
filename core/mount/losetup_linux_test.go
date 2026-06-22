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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/continuity/testutil"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

var randomData = []byte("randomdata")

func createTempFile(t *testing.T) string {
	t.Helper()

	f, err := os.Create(filepath.Join(t.TempDir(), "losetup"))
	require.NoError(t, err)
	t.Cleanup(func() {
		f.Close()
	})

	err = f.Truncate(512)
	require.NoError(t, err)

	return f.Name()
}

func TestNonExistingLoop(t *testing.T) {
	testutil.RequiresRoot(t)

	backingFile := "setup-loop-test-no-such-file"
	_, err := SetupLoop(backingFile, LoopParams{})
	if err == nil {
		t.Fatalf("setupLoop with non-existing file should fail")
	}
}

func TestRoLoop(t *testing.T) {
	testutil.RequiresRoot(t)

	backingFile := createTempFile(t)

	file, err := SetupLoop(backingFile, LoopParams{Readonly: true, Autoclear: true})
	require.NoError(t, err)
	t.Cleanup(func() {
		file.Close()
	})

	if _, err := file.Write(randomData); err == nil {
		t.Fatalf("writing to readonly loop device should fail")
	}
}

func TestRwLoop(t *testing.T) {
	testutil.RequiresRoot(t)

	backingFile := createTempFile(t)

	file, err := SetupLoop(backingFile, LoopParams{Autoclear: true})
	require.NoError(t, err)
	t.Cleanup(func() {
		file.Close()
	})

	if _, err := file.Write(randomData); err != nil {
		t.Fatal(err)
	}
}

func TestAttachDetachLoopDevice(t *testing.T) {
	testutil.RequiresRoot(t)

	path := createTempFile(t)

	dev, err := AttachLoopDevice(path)
	require.NoError(t, err)

	err = DetachLoopDevice(dev)
	require.NoError(t, err)
}

func TestAutoclearTrueLoop(t *testing.T) {
	testutil.RequiresRoot(t)

	backingFile := createTempFile(t)
	bInfo, err := os.Stat(backingFile)
	require.NoError(t, err)
	bInode := bInfo.Sys().(*syscall.Stat_t).Ino

	file, err := SetupLoop(backingFile, LoopParams{Autoclear: true})
	require.NoError(t, err)
	dev := file.Name()
	file.Close()

	var checkFn = func(loopDev string, expectedInode uint64) (_shouldRetry bool, _ error) {
		loop, err := os.Open(loopDev)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, fmt.Errorf("failed to open loop device: %w", err)
		}
		info, err := unix.IoctlLoopGetStatus64(int(loop.Fd()))
		loop.Close()
		if err != nil {
			if errors.Is(err, unix.ENXIO) {
				return false, nil
			}
			return false, fmt.Errorf("failed to get loop device info: %w", err)
		}

		if info.Inode != expectedInode {
			return false, nil
		}

		t.Logf("loop device %s still present with backing inode %d", loopDev, expectedInode)
		return true, nil
	}

	for range 10 {
		retry, err := checkFn(dev, bInode)
		require.NoError(t, err)
		if !retry {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("loop device %s still present after autoclear", dev)
}

func TestAutoclearFalseLoop(t *testing.T) {
	testutil.RequiresRoot(t)

	dev := func() string {
		backingFile := createTempFile(t)

		file, err := SetupLoop(backingFile, LoopParams{Autoclear: false})
		require.NoError(t, err)
		dev := file.Name()
		file.Close()
		return dev
	}()
	time.Sleep(100 * time.Millisecond)
	err := removeLoop(dev)
	require.NoError(t, err)
}
