//go:build !windows

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

package client

import (
	"encoding/json"
	"testing"

	"github.com/containerd/containerd/v2/internal/userns"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
)

func TestRemappedSnapshotID(t *testing.T) {
	newSnapshot := func() remappedSnapshot {
		return remappedSnapshot{
			Parent: digest.FromString("parent").String(),
			IDMap: userns.IDMap{
				UidMap: []specs.LinuxIDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
				GidMap: []specs.LinuxIDMapping{{ContainerID: 0, HostID: 200000, Size: 65536}},
			},
		}
	}
	supported, err := supportsNamespacedFileCapabilities()
	require.NoError(t, err)
	seen := map[string]bool{}
	for name, mutate := range map[string]func(*remappedSnapshot){
		"mapped":        func(s *remappedSnapshot) {},
		"other-uid":     func(s *remappedSnapshot) { s.IDMap.UidMap[0].HostID++ },
		"empty-parent":  func(s *remappedSnapshot) { s.Parent = "" },
		"identity-uid":  func(s *remappedSnapshot) { s.IDMap.UidMap = nil },
		"identity-gid":  func(s *remappedSnapshot) { s.IDMap.GidMap = nil },
		"identity-both": func(s *remappedSnapshot) { s.IDMap = userns.IDMap{} },
	} {
		t.Run(name, func(t *testing.T) {
			s := newSnapshot()
			mutate(&s)
			legacy, err := json.Marshal(&s)
			require.NoError(t, err)
			actual, err := s.ID()
			require.NoError(t, err)
			for mode, name := range map[bool]string{false: "stripped", true: "preserved"} {
				t.Run(name, func(t *testing.T) {
					id, err := s.id(mode)
					require.NoError(t, err)
					require.NoError(t, digest.Digest(id).Validate())
					require.NotEqual(t, digest.FromBytes(legacy).String(), id)
					again, err := s.id(mode)
					require.NoError(t, err)
					require.Equal(t, id, again)
					require.False(t, seen[id], "different parents, mappings and modes must have distinct keys")
					seen[id] = true
					if mode == supported {
						require.Equal(t, id, actual)
					}
				})
			}
		})
	}
	for name, mutate := range map[string]func(*remappedSnapshot){
		"invalid-parent": func(s *remappedSnapshot) { s.Parent = "not-a-digest" },
		"empty-uid":      func(s *remappedSnapshot) { s.IDMap.UidMap = []specs.LinuxIDMapping{} },
		"empty-gid":      func(s *remappedSnapshot) { s.IDMap.GidMap = []specs.LinuxIDMapping{} },
		"empty-range":    func(s *remappedSnapshot) { s.IDMap.UidMap[0].Size = 0 },
		"unmapped-root":  func(s *remappedSnapshot) { s.IDMap.UidMap[0].ContainerID = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			s := newSnapshot()
			mutate(&s)
			_, err := s.ID()
			require.Error(t, err)
		})
	}
}
