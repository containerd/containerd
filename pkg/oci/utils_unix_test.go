//go:build !windows && !darwin

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

package oci

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/moby/sys/userns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cleanupTest() {
	overrideDeviceFromPath = nil
	osReadDir = os.ReadDir
	usernsRunningInUserNS = userns.RunningInUserNS
}

// Based on test from runc:
// https://github.com/opencontainers/runc/blob/v1.0.0/libcontainer/devices/device_unix_test.go#L34-L47
func TestHostDevicesOSReadDirFailure(t *testing.T) {
	testError := fmt.Errorf("test error: %w", os.ErrPermission)

	// Override os.ReadDir to inject error.
	osReadDir = func(dirname string) ([]os.DirEntry, error) {
		return nil, testError
	}

	// Override userns.RunningInUserNS to ensure not running in user namespace.
	usernsRunningInUserNS = func() bool {
		return false
	}
	defer cleanupTest()

	_, err := HostDevices()
	if !errors.Is(err, testError) {
		t.Fatalf("Unexpected error %v, expected %v", err, testError)
	}
}

// Based on test from runc:
// https://github.com/opencontainers/runc/blob/v1.0.0/libcontainer/devices/device_unix_test.go#L34-L47
func TestHostDevicesOSReadDirFailureInUserNS(t *testing.T) {
	testError := fmt.Errorf("test error: %w", os.ErrPermission)

	// Override os.ReadDir to inject error.
	osReadDir = func(dirname string) ([]os.DirEntry, error) {
		if dirname == "/dev" {
			fi, err := os.Lstat("/dev/null")
			if err != nil {
				t.Fatalf("Unexpected error %v", err)
			}

			return []os.DirEntry{fs.FileInfoToDirEntry(fi)}, nil
		}
		return nil, testError
	}
	// Override userns.RunningInUserNS to ensure running in user namespace.
	usernsRunningInUserNS = func() bool {
		return true
	}
	defer cleanupTest()

	_, err := HostDevices()
	if !errors.Is(err, nil) {
		t.Fatalf("Unexpected error %v, expected %v", err, nil)
	}
}

// Based on test from runc:
// https://github.com/opencontainers/runc/blob/v1.0.0/libcontainer/devices/device_unix_test.go#L49-L74
func TestHostDevicesDeviceFromPathFailure(t *testing.T) {
	testError := fmt.Errorf("test error: %w", os.ErrPermission)

	// Override DeviceFromPath to produce an os.ErrPermission on /dev/null.
	overrideDeviceFromPath = func(path string) error {
		if path == "/dev/null" {
			return testError
		}
		return nil
	}

	// Override userns.RunningInUserNS to ensure not running in user namespace.
	usernsRunningInUserNS = func() bool {
		return false
	}
	defer cleanupTest()

	d, err := HostDevices()
	if !errors.Is(err, testError) {
		t.Fatalf("Unexpected error %v, expected %v", err, testError)
	}

	assert.Equal(t, 0, len(d))
}

// Based on test from runc:
// https://github.com/opencontainers/runc/blob/v1.0.0/libcontainer/devices/device_unix_test.go#L49-L74
func TestHostDevicesDeviceFromPathFailureInUserNS(t *testing.T) {
	testError := fmt.Errorf("test error: %w", os.ErrPermission)

	// Override DeviceFromPath to produce an os.ErrPermission on all devices,
	// except for /dev/null.
	overrideDeviceFromPath = func(path string) error {
		if path == "/dev/null" {
			return nil
		}
		return testError
	}

	// Override userns.RunningInUserNS to ensure running in user namespace.
	usernsRunningInUserNS = func() bool {
		return true
	}
	defer cleanupTest()

	d, err := HostDevices()
	if !errors.Is(err, nil) {
		t.Fatalf("Unexpected error %v, expected %v", err, nil)
	}
	assert.Equal(t, 1, len(d))
	assert.Equal(t, d[0].Path, "/dev/null")
}

func TestHostDevicesAllValid(t *testing.T) {
	devices, err := HostDevices()
	if err != nil {
		t.Fatalf("failed to get host devices: %v", err)
	}

	for _, device := range devices {
		if runtime.GOOS != "freebsd" {
			// On Linux, devices can't have major number 0.
			if device.Major == 0 {
				t.Errorf("device entry %+v has zero major number", device)
			}
		}
		switch device.Type {
		case blockDevice, charDevice:
		case fifoDevice:
			t.Logf("fifo devices shouldn't show up from HostDevices")
			fallthrough
		default:
			t.Errorf("device entry %+v has unexpected type %v", device, device.Type)
		}
	}
}

func TestSplitPathComponents(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty path",
			input: "",
			want:  []string{},
		},
		{
			name:  "single component",
			input: "passwd",
			want:  []string{"passwd"},
		},
		{
			name:  "relative path",
			input: "etc/passwd",
			want:  []string{"etc", "passwd"},
		},
		{
			name:  "absolute path",
			input: "/etc/passwd",
			want:  []string{"etc", "passwd"},
		},
		{
			name:  "repeated separators",
			input: "//a///b//c",
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "dot components are preserved",
			input: "a/./link/../real",
			want:  []string{"a", ".", "link", "..", "real"},
		},
		{
			name:  "trailing separator splits as dot",
			input: "a/b/",
			want:  []string{"a", "b", "."},
		},
		{
			name:  "root path",
			input: "/",
			want:  []string{"."},
		},
		{
			name:  "only separators",
			input: "///",
			want:  []string{"."},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, splitPathComponents(tc.input))
		})
	}
}

func TestResolveInRootFS(t *testing.T) {
	for _, tc := range []struct {
		name     string
		initOps  []fstest.Applier
		original string
		expected string
		err      string
	}{
		{
			name: "no symlink",
			initOps: []fstest.Applier{
				fstest.CreateDir("/dir/a/b", 0o755),
				fstest.CreateFile("/dir/a/b/c", nil, 0o644),
			},
			original: "/dir/a/b/c",
			expected: "dir/a/b/c",
		},
		{
			name: "symlink deadloop - v1",
			initOps: []fstest.Applier{
				fstest.CreateDir("/dir", 0o755),
				fstest.Symlink("b", "/dir/a"),
				fstest.Symlink("c", "/dir/b"),
				fstest.Symlink("a", "/dir/c"),
			},
			original: "/dir/a",
			err:      "too many levels of symbolic links",
		},
		{
			name: "symlink deadloop - v2",
			initOps: []fstest.Applier{
				fstest.CreateDir("/dir", 0o755),
				fstest.Symlink("/dir/b", "/dir/a"),
				fstest.Symlink("/dir/c", "/dir/b"),
				fstest.Symlink("/dir/a", "/dir/c"),
			},
			original: "/dir/a",
			err:      "too many levels of symbolic links",
		},
		{
			name: "symlink deadloop - v3",
			initOps: []fstest.Applier{
				fstest.CreateDir("/dir", 0o755),
				fstest.Symlink("loop", "/dir/loop"),
			},
			original: "/dir/loop",
			err:      "too many levels of symbolic links",
		},
		{
			name: "symlink deadloop - v4",
			initOps: []fstest.Applier{
				fstest.CreateDir("/dir", 0o755),
				fstest.Symlink("loop/..", "/dir/loop"),
			},
			original: "/dir/loop",
			err:      "too many levels of symbolic links",
		},
		{
			name: "symlink target component is too long",
			initOps: []fstest.Applier{
				fstest.CreateDir("/dir", 0o755),
				fstest.Symlink(strings.Repeat("a", 256), "/dir/link"),
			},
			original: "/dir/link",
			err:      "file name too long",
		},
		{
			name: "41 levels of symbolic links",
			initOps: func() []fstest.Applier {
				res := []fstest.Applier{
					fstest.CreateDir("/dir", 0o755),
					fstest.CreateFile("/dir/empty", nil, 0o644),
					fstest.Symlink("/dir/empty", "/dir/link1"),
				}

				for i := range 40 {
					res = append(res, fstest.Symlink(
						fmt.Sprintf("link%d", i+1),
						fmt.Sprintf("/dir/link%d", i+2)),
					)
				}
				return res
			}(),
			original: "/dir/link41",
			err:      "too many levels of symbolic links",
		},
		{
			name: "try to escape root",
			initOps: []fstest.Applier{
				fstest.CreateDir("/dir", 0o755),
				fstest.CreateDir("/real", 0o755),
				fstest.CreateFile("/real/empty", nil, 0o644),
				fstest.Symlink("../../../../../../real", "/dir/link"),
			},
			original: "/dir/link/empty",
			expected: "real/empty",
		},
		{
			name: "unfold symlink",
			initOps: []fstest.Applier{
				fstest.CreateDir("/dir", 0o755),
				fstest.CreateDir("/dir/l1", 0o755),
				fstest.CreateDir("/dir/l1/l2", 0o755),
				fstest.CreateFile("/dir/l1/empty", nil, 0o644),
				fstest.Symlink("l1/l2", "/dir/link"),
			},
			original: "/dir/link/../empty",
			expected: "dir/l1/empty",
		},
		{
			name: "not empty",
			initOps: []fstest.Applier{
				fstest.CreateDir("/dir", 0o755),
			},
			original: "./../.././.",
			expected: ".",
		},
		{
			name: "respect trailing slash - v1",
			initOps: []fstest.Applier{
				fstest.CreateDir("/dir", 0o755),
			},
			original: "./../.././dir/",
			expected: "dir",
		},
		{
			name: "respect trailing slash - v2",
			initOps: []fstest.Applier{
				fstest.CreateFile("/fake-dir", nil, 0o644),
			},
			original: "./../.././fake-dir/",
			err:      "not a directory",
		},
		{
			name:     "respect trailing slash - v3",
			initOps:  []fstest.Applier{},
			original: "/",
			expected: ".",
		},
		{
			name: "respect trailing slash - v4",
			initOps: []fstest.Applier{
				fstest.CreateFile("/f", nil, 0o644),
				fstest.Symlink("f", "/link"),
			},
			original: "link/",
			err:      "not a directory",
		},
		{
			// The symlink target's own trailing slash requires the
			// final entity to be a directory.
			name: "respect trailing slash - v5",
			initOps: []fstest.Applier{
				fstest.CreateFile("/f", nil, 0o644),
				fstest.Symlink("f/", "/link"),
			},
			original: "link",
			err:      "not a directory",
		},
		{
			// The trailing-slash requirement is sticky across
			// chained symlinks.
			name: "respect trailing slash - v6",
			initOps: []fstest.Applier{
				fstest.CreateFile("/f", nil, 0o644),
				fstest.Symlink("link2/", "/link1"),
				fstest.Symlink("f", "/link2"),
			},
			original: "link1",
			err:      "not a directory",
		},
		{
			name:     "empty path",
			initOps:  []fstest.Applier{},
			original: "",
			err:      "no such file or directory",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rootDir := t.TempDir()

			err := fstest.Apply(tc.initOps...).Apply(rootDir)
			require.NoError(t, err)

			rootFS, err := os.OpenRoot(rootDir)
			require.NoError(t, err)
			defer rootFS.Close()

			got, err := resolveInRootFS(rootFS.FS().(fs.ReadLinkFS), tc.original)
			if tc.err != "" {
				t.Logf("received %v", err)
				require.Error(t, err)
				assert.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expected, got)

			assert.True(t, fs.ValidPath(got))

			fd, err := rootFS.FS().Open(got)
			require.NoError(t, err)
			fd.Close()
		})
	}
}
