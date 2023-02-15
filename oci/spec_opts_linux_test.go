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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/continuity/fs/fstest"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

//nolint:gosec
func TestWithUserID(t *testing.T) {
	t.Parallel()

	expectedPasswd := `root:x:0:0:root:/root:/bin/ash
guest:x:405:100:guest:/dev/null:/sbin/nologin
`
	td := t.TempDir()
	apply := fstest.Apply(
		fstest.CreateDir("/etc", 0777),
		fstest.CreateFile("/etc/passwd", []byte(expectedPasswd), 0777),
	)
	if err := apply.Apply(td); err != nil {
		t.Fatalf("failed to apply: %v", err)
	}
	c := containers.Container{ID: t.Name()}
	testCases := []struct {
		userID      uint32
		expectedUID uint32
		expectedGID uint32
	}{
		{
			userID:      0,
			expectedUID: 0,
			expectedGID: 0,
		},
		{
			userID:      405,
			expectedUID: 405,
			expectedGID: 100,
		},
		{
			userID:      1000,
			expectedUID: 1000,
			expectedGID: 0,
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(fmt.Sprintf("user %d", testCase.userID), func(t *testing.T) {
			t.Parallel()
			s := Spec{
				Version: specs.Version,
				Root: &specs.Root{
					Path: td,
				},
				Linux: &specs.Linux{},
			}
			err := WithUserID(testCase.userID)(context.Background(), nil, &c, &s)
			assert.NoError(t, err)
			assert.Equal(t, testCase.expectedUID, s.Process.User.UID)
			assert.Equal(t, testCase.expectedGID, s.Process.User.GID)
		})
	}
}

//nolint:gosec
func TestWithUsername(t *testing.T) {
	t.Parallel()

	expectedPasswd := `root:x:0:0:root:/root:/bin/ash
guest:x:405:100:guest:/dev/null:/sbin/nologin
`
	td := t.TempDir()
	apply := fstest.Apply(
		fstest.CreateDir("/etc", 0777),
		fstest.CreateFile("/etc/passwd", []byte(expectedPasswd), 0777),
	)
	if err := apply.Apply(td); err != nil {
		t.Fatalf("failed to apply: %v", err)
	}
	c := containers.Container{ID: t.Name()}
	testCases := []struct {
		user        string
		expectedUID uint32
		expectedGID uint32
		err         string
	}{
		{
			user:        "root",
			expectedUID: 0,
			expectedGID: 0,
		},
		{
			user:        "guest",
			expectedUID: 405,
			expectedGID: 100,
		},
		{
			user: "1000",
			err:  "no users found",
		},
		{
			user: "unknown",
			err:  "no users found",
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.user, func(t *testing.T) {
			t.Parallel()
			s := Spec{
				Version: specs.Version,
				Root: &specs.Root{
					Path: td,
				},
				Linux: &specs.Linux{},
			}
			err := WithUsername(testCase.user)(context.Background(), nil, &c, &s)
			if err != nil {
				assert.EqualError(t, err, testCase.err)
			}
			assert.Equal(t, testCase.expectedUID, s.Process.User.UID)
			assert.Equal(t, testCase.expectedGID, s.Process.User.GID)
		})
	}

}

//nolint:gosec
func TestWithAdditionalGIDs(t *testing.T) {
	t.Parallel()
	expectedPasswd := `root:x:0:0:root:/root:/bin/ash
bin:x:1:1:bin:/bin:/sbin/nologin
daemon:x:2:2:daemon:/sbin:/sbin/nologin
`
	expectedGroup := `root:x:0:root
bin:x:1:root,bin,daemon
daemon:x:2:root,bin,daemon
sys:x:3:root,bin,adm
`
	td := t.TempDir()
	apply := fstest.Apply(
		fstest.CreateDir("/etc", 0777),
		fstest.CreateFile("/etc/passwd", []byte(expectedPasswd), 0777),
		fstest.CreateFile("/etc/group", []byte(expectedGroup), 0777),
	)
	if err := apply.Apply(td); err != nil {
		t.Fatalf("failed to apply: %v", err)
	}
	c := containers.Container{ID: t.Name()}

	testCases := []struct {
		user     string
		expected []uint32
	}{
		{
			user:     "root",
			expected: []uint32{0, 1, 2, 3},
		},
		{
			user:     "1000",
			expected: []uint32{0},
		},
		{
			user:     "bin",
			expected: []uint32{0, 2, 3},
		},
		{
			user:     "bin:root",
			expected: []uint32{0},
		},
		{
			user:     "daemon",
			expected: []uint32{0, 1},
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.user, func(t *testing.T) {
			t.Parallel()
			s := Spec{
				Version: specs.Version,
				Root: &specs.Root{
					Path: td,
				},
			}
			err := WithAdditionalGIDs(testCase.user)(context.Background(), nil, &c, &s)
			assert.NoError(t, err)
			assert.Equal(t, testCase.expected, s.Process.User.AdditionalGids)
		})
	}
}

func TestAddCaps(t *testing.T) {
	t.Parallel()

	var s specs.Spec

	if err := WithAddedCapabilities([]string{"CAP_CHOWN"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	for i, cl := range [][]string{
		s.Process.Capabilities.Bounding,
		s.Process.Capabilities.Effective,
		s.Process.Capabilities.Permitted,
	} {
		if !capsContain(cl, "CAP_CHOWN") {
			t.Errorf("cap list %d does not contain added cap", i)
		}
	}
}

func TestDropCaps(t *testing.T) {
	t.Parallel()

	var s specs.Spec

	if err := WithAllKnownCapabilities(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	if err := WithDroppedCapabilities([]string{"CAP_CHOWN"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}

	for i, cl := range [][]string{
		s.Process.Capabilities.Bounding,
		s.Process.Capabilities.Effective,
		s.Process.Capabilities.Permitted,
	} {
		if capsContain(cl, "CAP_CHOWN") {
			t.Errorf("cap list %d contains dropped cap", i)
		}
	}

	// Add all capabilities back and drop a different cap.
	if err := WithAllKnownCapabilities(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	if err := WithDroppedCapabilities([]string{"CAP_FOWNER"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}

	for i, cl := range [][]string{
		s.Process.Capabilities.Bounding,
		s.Process.Capabilities.Effective,
		s.Process.Capabilities.Permitted,
	} {
		if capsContain(cl, "CAP_FOWNER") {
			t.Errorf("cap list %d contains dropped cap", i)
		}
		if !capsContain(cl, "CAP_CHOWN") {
			t.Errorf("cap list %d doesn't contain non-dropped cap", i)
		}
	}

	// Drop all duplicated caps.
	if err := WithCapabilities([]string{"CAP_CHOWN", "CAP_CHOWN"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	if err := WithDroppedCapabilities([]string{"CAP_CHOWN"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	for i, cl := range [][]string{
		s.Process.Capabilities.Bounding,
		s.Process.Capabilities.Effective,
		s.Process.Capabilities.Permitted,
	} {
		if len(cl) != 0 {
			t.Errorf("cap list %d is not empty", i)
		}
	}
}

func TestGetDevices(t *testing.T) {
	testutil.RequiresRoot(t)

	dir, err := os.MkdirTemp("/dev", t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	zero := filepath.Join(dir, "zero")
	if err := os.WriteFile(zero, nil, 0600); err != nil {
		t.Fatal(err)
	}

	if err := unix.Mount("/dev/zero", zero, "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	defer unix.Unmount(filepath.Join(dir, "zero"), unix.MNT_DETACH)

	t.Run("single device", func(t *testing.T) {
		t.Run("no container path", func(t *testing.T) {
			devices, err := getDevices(dir, "")
			if err != nil {
				t.Fatal(err)
			}

			if len(devices) != 1 {
				t.Fatalf("expected one device %v", devices)
			}
			if devices[0].Path != zero {
				t.Fatalf("got unexpected device path %s", devices[0].Path)
			}
		})
		t.Run("with container path", func(t *testing.T) {
			newPath := "/dev/testNew"
			devices, err := getDevices(dir, newPath)
			if err != nil {
				t.Fatal(err)
			}

			if len(devices) != 1 {
				t.Fatalf("expected one device %v", devices)
			}
			if devices[0].Path != filepath.Join(newPath, "zero") {
				t.Fatalf("got unexpected device path %s", devices[0].Path)
			}
		})
	})
	t.Run("two devices", func(t *testing.T) {
		nullDev := filepath.Join(dir, "null")
		if err := os.WriteFile(nullDev, nil, 0600); err != nil {
			t.Fatal(err)
		}

		if err := unix.Mount("/dev/null", nullDev, "", unix.MS_BIND, ""); err != nil {
			t.Fatal(err)
		}
		defer unix.Unmount(filepath.Join(dir, "null"), unix.MNT_DETACH)
		devices, err := getDevices(dir, "")
		if err != nil {
			t.Fatal(err)
		}

		if len(devices) != 2 {
			t.Fatalf("expected two devices %v", devices)
		}
		if devices[0].Path == devices[1].Path {
			t.Fatalf("got same path for the two devices %s", devices[0].Path)
		}
		if devices[0].Path != zero && devices[0].Path != nullDev {
			t.Fatalf("got unexpected device path %s", devices[0].Path)
		}
		if devices[1].Path != zero && devices[1].Path != nullDev {
			t.Fatalf("got unexpected device path %s", devices[1].Path)
		}
		if devices[0].Major == devices[1].Major && devices[0].Minor == devices[1].Minor {
			t.Fatalf("got sema mojor and minor on two devices %s %s", devices[0].Path, devices[1].Path)
		}
	})
	t.Run("With symlink in dir", func(t *testing.T) {
		if err := os.Symlink("/dev/zero", filepath.Join(dir, "zerosym")); err != nil {
			t.Fatal(err)
		}
		devices, err := getDevices(dir, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(devices) != 1 {
			t.Fatalf("expected one device %v", devices)
		}
		if devices[0].Path != filepath.Join(dir, "zero") {
			t.Fatalf("got unexpected device path, expected %q, got %q", filepath.Join(dir, "zero"), devices[0].Path)
		}
	})
	t.Run("No devices", func(t *testing.T) {
		dir := dir + "2"
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)

		t.Run("empty dir", func(T *testing.T) {
			devices, err := getDevices(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(devices) != 0 {
				t.Fatalf("expected no devices, got %+v", devices)
			}
		})
		t.Run("symlink to device in dir", func(t *testing.T) {
			if err := os.Symlink("/dev/zero", filepath.Join(dir, "zerosym")); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(filepath.Join(dir, "zerosym"))

			devices, err := getDevices(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(devices) != 0 {
				t.Fatalf("expected no devices, got %+v", devices)
			}
		})
		t.Run("regular file in dir", func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "somefile"), []byte("hello"), 0600); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(filepath.Join(dir, "somefile"))

			devices, err := getDevices(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(devices) != 0 {
				t.Fatalf("expected no devices, got %+v", devices)
			}
		})
	})
}

func TestWithAppendAdditionalGroups(t *testing.T) {
	t.Parallel()
	expectedContent := `root:x:0:root
bin:x:1:root,bin,daemon
daemon:x:2:root,bin,daemon
`
	td := t.TempDir()
	apply := fstest.Apply(
		fstest.CreateDir("/etc", 0777),
		fstest.CreateFile("/etc/group", []byte(expectedContent), 0777),
	)
	if err := apply.Apply(td); err != nil {
		t.Fatalf("failed to apply: %v", err)
	}
	c := containers.Container{ID: t.Name()}

	testCases := []struct {
		name           string
		additionalGIDs []uint32
		groups         []string
		expected       []uint32
		err            string
	}{
		{
			name:     "no additional gids",
			groups:   []string{},
			expected: []uint32{0},
		},
		{
			name:     "no additional gids, append root gid",
			groups:   []string{"root"},
			expected: []uint32{0},
		},
		{
			name:     "no additional gids, append bin and daemon gids",
			groups:   []string{"bin", "daemon"},
			expected: []uint32{0, 1, 2},
		},
		{
			name:           "has root additional gids, append bin and daemon gids",
			additionalGIDs: []uint32{0},
			groups:         []string{"bin", "daemon"},
			expected:       []uint32{0, 1, 2},
		},
		{
			name:     "append group id",
			groups:   []string{"999"},
			expected: []uint32{0, 999},
		},
		{
			name:     "unknown group",
			groups:   []string{"unknown"},
			err:      "unable to find group unknown",
			expected: []uint32{0},
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			s := Spec{
				Version: specs.Version,
				Root: &specs.Root{
					Path: td,
				},
				Process: &specs.Process{
					User: specs.User{
						AdditionalGids: testCase.additionalGIDs,
					},
				},
			}
			err := WithAppendAdditionalGroups(testCase.groups...)(context.Background(), nil, &c, &s)
			if err != nil {
				assert.EqualError(t, err, testCase.err)
			}
			assert.Equal(t, testCase.expected, s.Process.User.AdditionalGids)
		})
	}
}
func TestWithLinuxDeviceFollowSymlinks(t *testing.T) {

	// Create symlink to /dev/zero for the symlink test case
	zero := "/dev/zero"
	_, err := os.Stat(zero)
	require.NoError(t, err, "Host does not have /dev/zero")

	dir := t.TempDir()
	symZero := filepath.Join(dir, "zero")

	err = os.Symlink(zero, symZero)
	require.NoError(t, err, "unexpected error creating symlink")

	testcases := []struct {
		name           string
		path           string
		followSymlinks bool

		expectError          bool
		expectedLinuxDevices []specs.LinuxDevice
	}{
		{
			name:        "regularDeviceresolvesPath",
			path:        zero,
			expectError: false,
			expectedLinuxDevices: []specs.LinuxDevice{{
				Path: zero,
				Type: "c",
			}},
		},
		{
			name:        "symlinkedDeviceResolvesPath",
			path:        symZero,
			expectError: false,
			expectedLinuxDevices: []specs.LinuxDevice{{
				Path: zero,
				Type: "c",
			}},
		},
		{
			name:        "symlinkedDeviceResolvesFakePath_error",
			path:        "/fake/path",
			expectError: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			spec := Spec{
				Version: specs.Version,
				Root:    &specs.Root{},
				Linux:   &specs.Linux{},
			}

			opts := []SpecOpts{
				WithLinuxDeviceFollowSymlinks(tc.path, ""),
			}

			for _, opt := range opts {
				if err := opt(nil, nil, nil, &spec); err != nil {
					if tc.expectError {
						assert.Error(t, err)
					} else {
						assert.NoError(t, err)
					}
				}
			}
			if len(tc.expectedLinuxDevices) != 0 {
				require.NotNil(t, spec.Linux)
				require.Len(t, spec.Linux.Devices, 1)

				assert.Equal(t, spec.Linux.Devices[0].Path, tc.expectedLinuxDevices[0].Path)
				assert.Equal(t, spec.Linux.Devices[0].Type, tc.expectedLinuxDevices[0].Type)
			}
		})
	}
}
