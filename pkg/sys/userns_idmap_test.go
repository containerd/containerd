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

package sys

import (
	"os"
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
)

func TestRootPair(t *testing.T) {
	idmap := IdentityMapping{
		UIDMaps: []specs.LinuxIDMapping{
			{
				ContainerID: 0,
				HostID:      uint32(os.Getuid()),
				Size:        1,
			},
		},
		GIDMaps: []specs.LinuxIDMapping{
			{
				ContainerID: 0,
				HostID:      uint32(os.Getgid()),
				Size:        1,
			},
		},
	}

	identity, err := idmap.RootPair()
	assert.NoError(t, err)
	assert.Equal(t, uint32(os.Geteuid()), identity.UID)
	assert.Equal(t, uint32(os.Getegid()), identity.GID)

	idmapErr := IdentityMapping{
		UIDMaps: []specs.LinuxIDMapping{
			{
				ContainerID: 1,
				HostID:      uint32(os.Getuid()),
				Size:        1,
			},
		},
	}
	_, err = idmapErr.RootPair()
	assert.EqualError(t, err, "Container ID 0 cannot be mapped to a host ID")
}

func TestToContainer(t *testing.T) {
	idmap := IdentityMapping{
		UIDMaps: []specs.LinuxIDMapping{
			{
				ContainerID: 0,
				HostID:      1,
				Size:        2,
			},
			{
				ContainerID: 2,
				HostID:      4,
				Size:        1000,
			},
		},
		GIDMaps: []specs.LinuxIDMapping{
			{
				ContainerID: 0,
				HostID:      2,
				Size:        4,
			},
			{
				ContainerID: 4,
				HostID:      8,
				Size:        1000,
			},
		},
	}
	for _, tt := range []struct {
		cid Identity
		hid Identity
	}{
		{
			hid: Identity{
				UID: 1,
				GID: 2,
			},
			cid: Identity{
				UID: 0,
				GID: 0,
			},
		},
		{
			hid: Identity{
				UID: 2,
				GID: 3,
			},
			cid: Identity{
				UID: 1,
				GID: 1,
			},
		},
		{
			hid: Identity{
				UID: 4,
				GID: 8,
			},
			cid: Identity{
				UID: 2,
				GID: 4,
			},
		},
		{
			hid: Identity{
				UID: 102,
				GID: 204,
			},
			cid: Identity{
				UID: 100,
				GID: 200,
			},
		},
		{
			hid: Identity{
				UID: 1003,
				GID: 1007,
			},
			cid: Identity{
				UID: 1001,
				GID: 1003,
			},
		},
		{
			hid: Identity{
				UID: 1004,
				GID: 1008,
			},
			cid: badIdentify,
		},
		{
			hid: Identity{
				UID: 2000,
				GID: 2000,
			},
			cid: badIdentify,
		},
	} {
		r, err := idmap.ToContainer(tt.hid)
		assert.Equal(t, tt.cid, r)
		if r == badIdentify {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
		}
	}
}

func TestToHost(t *testing.T) {
	idmap := IdentityMapping{
		UIDMaps: []specs.LinuxIDMapping{
			{
				ContainerID: 0,
				HostID:      1,
				Size:        2,
			},
			{
				ContainerID: 2,
				HostID:      4,
				Size:        1000,
			},
		},
		GIDMaps: []specs.LinuxIDMapping{
			{
				ContainerID: 0,
				HostID:      2,
				Size:        4,
			},
			{
				ContainerID: 4,
				HostID:      8,
				Size:        1000,
			},
		},
	}
	for _, tt := range []struct {
		cid Identity
		hid Identity
	}{
		{
			cid: Identity{
				UID: 0,
				GID: 0,
			},
			hid: Identity{
				UID: 1,
				GID: 2,
			},
		},
		{
			cid: Identity{
				UID: 1,
				GID: 1,
			},
			hid: Identity{
				UID: 2,
				GID: 3,
			},
		},
		{
			cid: Identity{
				UID: 2,
				GID: 4,
			},
			hid: Identity{
				UID: 4,
				GID: 8,
			},
		},
		{
			cid: Identity{
				UID: 100,
				GID: 200,
			},
			hid: Identity{
				UID: 102,
				GID: 204,
			},
		},
		{
			cid: Identity{
				UID: 1001,
				GID: 1003,
			},
			hid: Identity{
				UID: 1003,
				GID: 1007,
			},
		},
		{
			cid: Identity{
				UID: 1004,
				GID: 1008,
			},
			hid: badIdentify,
		},
		{
			cid: Identity{
				UID: 2000,
				GID: 2000,
			},
			hid: badIdentify,
		},
	} {
		r, err := idmap.ToHost(tt.cid)
		assert.Equal(t, tt.hid, r)
		if r == badIdentify {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
		}
	}
}
