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
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/pkg/testutil"
	"github.com/opencontainers/runtime-spec/specs-go"
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
			dir, err := ioutil.TempDir("", "test-new-bundle")
			if err != nil {
				t.Fatal("failed to create test directory", err)
			}
			defer os.RemoveAll(dir)
			work := filepath.Join(dir, "work")
			state := filepath.Join(dir, "state")
			id := fmt.Sprintf("new-bundle-%d", i)
			spec := oci.Spec{}
			if tc.userns {
				spec.Linux = &specs.Linux{
					GIDMappings: []specs.LinuxIDMapping{{ContainerID: 0, HostID: usernsGID}},
				}
			}
			specBytes, err := json.Marshal(&spec)
			if err != nil {
				t.Fatal("failed to marshal spec", err)
			}

			ctx := namespaces.WithNamespace(context.TODO(), namespaces.Default)
			b, err := NewBundle(ctx, work, state, id, specBytes)
			if err != nil {
				t.Fatal("NewBundle should succeed", err)
			}
			if b == nil {
				t.Fatal("bundle should not be nil")
			}

			fi, err := os.Stat(b.Path)
			if err != nil {
				t.Error("should be able to stat bundle path", err)
			}
			if tc.userns {
				if fi.Mode() != os.ModeDir|0710 {
					t.Error("bundle path should be a directory with perm 0710")
				}
			} else {
				if fi.Mode() != os.ModeDir|0700 {
					t.Error("bundle path should be a directory with perm 0700")
				}
			}
			stat, ok := fi.Sys().(*syscall.Stat_t)
			if !ok {
				t.Fatal("should assert to *syscall.Stat_t")
			}
			expectedGID := uint32(0)
			if tc.userns {
				expectedGID = usernsGID
			}
			if expectedGID != stat.Gid {
				t.Error("gid should match", expectedGID, stat.Gid)
			}
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
			if err != nil {
				t.Fatal("failed to marshal spec", err)
			}
			gid, err := remappedGID(s)
			if err != nil {
				t.Error("should unmarshal successfully", err)
			}
			if tc.gid != gid {
				t.Error("expected GID to match", tc.gid, gid)
			}
		})
	}
}
