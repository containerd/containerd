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

package opts

import (
	"context"
	"testing"

	"github.com/containerd/continuity/fs/fstest"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"

	"github.com/containerd/containerd/v2/core/containers"
)

// TestWithAdditionalGIDsSharedUserGroupName reproduces
// https://github.com/containerd/containerd/issues/11937 for the CRI codepath.
//
// The CRI WithAdditionalGIDs wraps oci.WithAdditionalGIDs, so it shares the
// same bug: the container user "name" (uid 2, primary gid 1/daemon) is a
// member of group "name" (gid 2), which is therefore a supplemental group and
// must appear in the additional GID list. containerd currently drops it
// because a group whose name matches the user's name is assumed to be that
// user's primary group.
func TestWithAdditionalGIDsSharedUserGroupName(t *testing.T) {
	expectedPasswd := `name:x:2:1::/home/name:/usr/sbin/nologin
`
	expectedGroup := `daemon:x:1:
name:x:2:name
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
		// Resolve the supplemental groups by username.
		{
			user:     "name",
			expected: []uint32{1, 2},
		},
		// Resolving by uid must produce the same result.
		{
			user:     "2",
			expected: []uint32{1, 2},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.user, func(t *testing.T) {
			s := &runtimespec.Spec{
				Version: runtimespec.Version,
				Root: &runtimespec.Root{
					Path: td,
				},
				Process: &runtimespec.Process{
					User: runtimespec.User{
						UID: 2,
						GID: 1,
					},
				},
			}
			err := WithAdditionalGIDs(testCase.user)(context.Background(), nil, &c, s)
			assert.NoError(t, err)
			assert.Equal(t, testCase.expected, s.Process.User.AdditionalGids)
		})
	}
}
