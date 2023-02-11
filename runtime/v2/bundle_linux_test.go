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

package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBundle(t *testing.T) {
	testutil.RequiresRoot(t)
	tests := []struct {
		userns bool
	}{{
		userns: false,
	}, {
		userns: true,
	}}
	const usernsGID = 4200

	for i, tc := range tests {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			dir := t.TempDir()
			work := filepath.Join(dir, "work")
			state := filepath.Join(dir, "state")
			id := fmt.Sprintf("new-bundle-%d", i)
			spec := oci.Spec{}
			if tc.userns {
				spec.Linux = &specs.Linux{
					GIDMappings: []specs.LinuxIDMapping{{ContainerID: 0, HostID: usernsGID}},
				}
			}
			specAny, err := typeurl.MarshalAny(&spec)
			require.NoError(t, err, "failed to marshal spec")

			ctx := namespaces.WithNamespace(context.TODO(), namespaces.Default)
			b, err := NewBundle(ctx, work, state, id, specAny)
			require.NoError(t, err, "NewBundle should succeed")
			require.NotNil(t, b, "bundle should not be nil")

			fi, err := os.Stat(b.Path)
			assert.NoError(t, err, "should be able to stat bundle path")
			if tc.userns {
				assert.Equal(t, os.ModeDir|0710, fi.Mode(), "bundle path should be a directory with perm 0710")
			} else {
				assert.Equal(t, os.ModeDir|0700, fi.Mode(), "bundle path should be a directory with perm 0700")
			}
			stat, ok := fi.Sys().(*syscall.Stat_t)
			require.True(t, ok, "should assert to *syscall.Stat_t")
			expectedGID := uint32(0)
			if tc.userns {
				expectedGID = usernsGID
			}
			assert.Equal(t, expectedGID, stat.Gid, "gid should match")

		})
	}
}

func TestRemappedGID(t *testing.T) {
	tests := []struct {
		spec oci.Spec
		gid  uint32
	}{{
		// empty spec
		spec: oci.Spec{},
		gid:  0,
	}, {
		// empty Linux section
		spec: oci.Spec{
			Linux: &specs.Linux{},
		},
		gid: 0,
	}, {
		// empty ID mappings
		spec: oci.Spec{
			Linux: &specs.Linux{
				GIDMappings: make([]specs.LinuxIDMapping, 0),
			},
		},
		gid: 0,
	}, {
		// valid ID mapping
		spec: oci.Spec{
			Linux: &specs.Linux{
				GIDMappings: []specs.LinuxIDMapping{{
					ContainerID: 0,
					HostID:      1000,
				}},
			},
		},
		gid: 1000,
	}, {
		// missing ID mapping
		spec: oci.Spec{
			Linux: &specs.Linux{
				GIDMappings: []specs.LinuxIDMapping{{
					ContainerID: 100,
					HostID:      1000,
				}},
			},
		},
		gid: 0,
	}}

	for i, tc := range tests {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			s, err := json.Marshal(tc.spec)
			require.NoError(t, err, "failed to marshal spec")
			gid, err := remappedGID(s)
			assert.NoError(t, err, "should unmarshal successfully")
			assert.Equal(t, tc.gid, gid, "expected GID to match")
		})
	}
}
