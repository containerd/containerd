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
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

type fMountatCaseFunc func(t *testing.T, root string)

func TestFMountat(t *testing.T) {
	if unix.Geteuid() != 0 {
		t.Skip("Needs to be run as root")
		return
	}

	t.Run("Normal", makeTestForFMountat(testFMountatNormal))
	t.Run("ChdirWithFileFd", makeTestForFMountat(testFMountatWithFileFd))
	t.Run("MountWithInvalidSource", makeTestForFMountat(testFMountatWithInvalidSource))
}

func makeTestForFMountat(fn fMountatCaseFunc) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		suiteDir, err := os.MkdirTemp("", "fmountat-test-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(suiteDir)

		fn(t, suiteDir)
	}
}

func testFMountatNormal(t *testing.T, root string) {
	expectedContent := "bye re-exec!\n"
	apply := fstest.Apply(
		fstest.CreateFile("/hi", []byte(expectedContent), 0777),
	)

	workdir := filepath.Join(root, "work")
	if err := os.MkdirAll(workdir, 0777); err != nil {
		t.Fatalf("failed to create dir(%s): %+v", workdir, err)
	}

	if err := apply.Apply(workdir); err != nil {
		t.Fatalf("failed to prepare source dir: %+v", err)
	}

	atdir := filepath.Join(root, "at")
	if err := os.MkdirAll(atdir, 0777); err != nil {
		t.Fatalf("failed to create working dir(%s): %+v", atdir, err)
	}

	fsdir := filepath.Join(atdir, "fs")
	if err := os.MkdirAll(fsdir, 0777); err != nil {
		t.Fatalf("failed to create mount point dir(%s): %+v", fsdir, err)
	}

	f, err := os.Open(atdir)
	if err != nil {
		t.Fatalf("failed to open dir(%s): %+v", atdir, err)
	}
	defer f.Close()

	// mount work to fs
	if err = fMountat(f.Fd(), workdir, "fs", "bind", unix.MS_BIND|unix.MS_RDONLY, ""); err != nil {
		t.Fatalf("expected no error here, but got error: %+v", err)
	}
	defer umount(t, fsdir)

	// check hi file
	content, err := os.ReadFile(filepath.Join(fsdir, "hi"))
	if err != nil {
		t.Fatalf("failed to read file: %+v", err)
	}
	if got := string(content); got != expectedContent {
		t.Fatalf("expected to get(%v), but got(%v)", expectedContent, got)
	}

	// check the working directory
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working dir: %+v", err)
	}

	if cwd == atdir {
		t.Fatal("should not change the current working directory")
	}
}

func testFMountatWithFileFd(t *testing.T, root string) {
	// not a directory
	expectedErr := syscall.Errno(20)

	emptyFile := filepath.Join(root, "emptyFile")
	f, err := os.Create(emptyFile)
	if err != nil {
		t.Fatalf("failed to create file(%s): %+v", emptyFile, err)
	}
	defer f.Close()

	err = fMountat(f.Fd(), filepath.Join(root, "empty"), filepath.Join(root, "work"), "", 0, "")
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, but got %v", expectedErr, errors.Cause(err))
	}
}

func testFMountatWithInvalidSource(t *testing.T, root string) {
	// no such file or directory
	expectedErr := syscall.Errno(2)

	atdir := filepath.Join(root, "at")
	if err := os.MkdirAll(atdir, 0777); err != nil {
		t.Fatalf("failed to create dir(%s): %+v", atdir, err)
	}

	f, err := os.Open(root)
	if err != nil {
		t.Fatalf("failed to open dir(%s): %+v", atdir, err)
	}
	defer f.Close()

	err = fMountat(f.Fd(), filepath.Join(root, "oops"), "at", "bind", unix.MS_BIND, "")
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, but got %v", expectedErr, err)
	}
}

func umount(t *testing.T, target string) {
	for i := 0; i < 50; i++ {
		if err := unix.Unmount(target, unix.MNT_DETACH); err != nil {
			switch err {
			case unix.EBUSY:
				time.Sleep(50 * time.Millisecond)
				continue
			case unix.EINVAL:
				return
			default:
				continue
			}
		}
	}
	t.Fatalf("failed to unmount target %s", target)
}
