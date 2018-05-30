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

package specconv

import (
	"testing"

	"github.com/opencontainers/runc/libcontainer/specconv"
	"github.com/opencontainers/runc/libcontainer/user"
	"github.com/opencontainers/runtime-spec/specs-go"
	"gotest.tools/assert"
	is "gotest.tools/assert/cmp"
)

func TestToRootless(t *testing.T) {
	spec := specconv.Example()
	uidMap := []user.IDMap{
		{
			ID:       0,
			ParentID: 4242,
			Count:    1,
		},
		{
			ID:       1,
			ParentID: 231072,
			Count:    65536,
		},
	}
	gidMap := uidMap
	expectedUIDMappings := []specs.LinuxIDMapping{
		{
			HostID:      0,
			ContainerID: 0,
			Size:        1,
		},
		{
			HostID:      1,
			ContainerID: 1,
			Size:        65536,
		},
	}
	err := toRootless(spec, uidMap, gidMap)
	assert.NilError(t, err)
	assert.Assert(t, is.DeepEqual(expectedUIDMappings, spec.Linux.UIDMappings))
}
