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
	"testing"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"

	"github.com/containerd/containerd/v2/core/containers"
)

//nolint:gosec
func TestWithUser(t *testing.T) {
	t.Parallel()

	expectedPasswd := `root:x:0:0:root:/root:/bin/ash
guest:x:405:100:guest:/dev/null:/sbin/nologin
`
	expectedGroup := `root:x:0:root
bin:x:1:root,bin,daemon
daemon:x:2:root,bin,daemon
sys:x:3:root,bin,adm
guest:x:100:guest
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
		user        string
		expectedUID uint32
		expectedGID uint32
		err         string
	}{
		{
			user:        "0",
			expectedUID: 0,
			expectedGID: 0,
		},
		{
			user:        "root:root",
			expectedUID: 0,
			expectedGID: 0,
		},
		{
			user:        "guest",
			expectedUID: 405,
			expectedGID: 100,
		},
		{
			user:        "guest:guest",
			expectedUID: 405,
			expectedGID: 100,
		},
		{
			user: "guest:nobody",
			err:  "no groups found",
		},
		{
			user:        "405:100",
			expectedUID: 405,
			expectedGID: 100,
		},
		{
			user: "405:2147483648",
			err:  "no groups found",
		},
		{
			user: "-1000",
			err:  "no users found",
		},
		{
			user: "2147483648",
			err:  "no users found",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.user, func(t *testing.T) {
			t.Parallel()
			s := Spec{
				Version: specs.Version,
				Root: &specs.Root{
					Path: td,
				},
				Linux: &specs.Linux{},
			}
			err := WithUser(testCase.user)(context.Background(), nil, &c, &s)
			if err != nil {
				assert.EqualError(t, err, testCase.err)
			}
			assert.Equal(t, testCase.expectedUID, s.Process.User.UID)
			assert.Equal(t, testCase.expectedGID, s.Process.User.GID)
		})
	}
}

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

func TestWithAppendAdditionalGroupsNoEtcGroup(t *testing.T) {
	t.Parallel()
	td := t.TempDir()
	apply := fstest.Apply()
	if err := apply.Apply(td); err != nil {
		t.Fatalf("failed to apply: %v", err)
	}
	c := containers.Container{ID: t.Name()}

	testCases := []struct {
		name           string
		additionalGIDs []uint32
		groups         []string
		expected       []uint32
		errContains    string
	}{
		{
			name:     "no additional gids",
			groups:   []string{},
			expected: []uint32{0},
		},
		{
			name:        "no additional gids, append root group",
			groups:      []string{"root"},
			errContains: "unable to find group root",
			expected:    []uint32{0},
		},
		{
			name:     "append group id",
			groups:   []string{"999"},
			expected: []uint32{0, 999},
		},
	}

	for _, testCase := range testCases {
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
				assert.ErrorContains(t, err, testCase.errContains)
			}
			assert.Equal(t, testCase.expected, s.Process.User.AdditionalGids)
		})
	}
}
