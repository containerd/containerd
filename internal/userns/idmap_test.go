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

package userns

import (
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
)

func TestRootPair(t *testing.T) {
	for _, test := range []struct {
		idmap  IDMap
		expUID uint32
		expGID uint32
		expErr bool
	}{
		{
			idmap: IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      100,
						Size:        1,
					},
				},
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      200,
						Size:        1,
					},
				},
			},
			expUID: 100,
			expGID: 200,
			expErr: false,
		},
		{
			idmap: IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 1,
						HostID:      100,
						Size:        1,
					},
				},
			},
			expUID: invalidID,
			expGID: invalidID,
			expErr: true,
		},
	} {
		user, err := test.idmap.RootPair()
		assert.Equal(t, test.expErr, err != nil)
		assert.Equal(t, test.expUID, user.Uid)
		assert.Equal(t, test.expGID, user.Gid)
	}
}

func TestToHost(t *testing.T) {
	idmap := IDMap{
		UidMap: []specs.LinuxIDMapping{
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
		GidMap: []specs.LinuxIDMapping{
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
	for _, test := range []struct {
		container User
		host      User
	}{
		{
			container: User{
				Uid: 0,
				Gid: 0,
			},
			host: User{
				Uid: 1,
				Gid: 2,
			},
		},
		{
			container: User{
				Uid: 1,
				Gid: 1,
			},
			host: User{
				Uid: 2,
				Gid: 3,
			},
		},
		{
			container: User{
				Uid: 2,
				Gid: 4,
			},
			host: User{
				Uid: 4,
				Gid: 8,
			},
		},
		{
			container: User{
				Uid: 100,
				Gid: 200,
			},
			host: User{
				Uid: 102,
				Gid: 204,
			},
		},
		{
			container: User{
				Uid: 1001,
				Gid: 1003,
			},
			host: User{
				Uid: 1003,
				Gid: 1007,
			},
		},
		{
			container: User{
				Uid: 1004,
				Gid: 1008,
			},
			host: invalidUser,
		},
		{
			container: User{
				Uid: 2000,
				Gid: 2000,
			},
			host: invalidUser,
		},
	} {
		r, err := idmap.ToHost(test.container)
		assert.Equal(t, test.host, r)
		if r == invalidUser {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
		}
	}
}

func TestToHostOverflow(t *testing.T) {
	for _, test := range []struct {
		idmap IDMap
		user  User
	}{
		{
			idmap: IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 1<<32 - 1000,
						HostID:      1000,
						Size:        10000,
					},
				},
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1000,
						Size:        10000,
					},
				},
			},
			user: User{
				Uid: 1<<32 - 100,
				Gid: 0,
			},
		},
		{
			idmap: IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1000,
						Size:        10000,
					},
				},
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 1<<32 - 1000,
						HostID:      1000,
						Size:        10000,
					},
				},
			},
			user: User{
				Uid: 0,
				Gid: 1<<32 - 100,
			},
		},
		{
			idmap: IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1000,
						Size:        1<<32 - 1,
					},
				},
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1000,
						Size:        1<<32 - 1,
					},
				},
			},
			user: User{
				Uid: 1<<32 - 2,
				Gid: 0,
			},
		},
		{
			idmap: IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1000,
						Size:        1<<32 - 1,
					},
				},
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1000,
						Size:        1<<32 - 1,
					},
				},
			},
			user: User{
				Uid: 0,
				Gid: 1<<32 - 2,
			},
		},
		{
			idmap: IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1,
						Size:        1<<32 - 1,
					},
				},
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1,
						Size:        1<<32 - 1,
					},
				},
			},
			user: User{
				Uid: 1<<32 - 2,
				Gid: 1<<32 - 2,
			},
		},
	} {
		r, err := test.idmap.ToHost(test.user)
		assert.Error(t, err)
		assert.Equal(t, r, invalidUser)
	}
}

func TestMarshal(t *testing.T) {
	for _, test := range []struct {
		idmap  IDMap
		expUID string
		expGID string
	}{
		{
			idmap: IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1000,
						Size:        10000,
					},
					{
						ContainerID: 1000,
						HostID:      20000,
						Size:        10000,
					},
				},
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1000,
						Size:        1000,
					},
					{
						ContainerID: 1000,
						HostID:      1000,
						Size:        10000,
					},
				},
			},
			expUID: "0:1000:10000,1000:20000:10000",
			expGID: "0:1000:1000,1000:1000:10000",
		},
		{
			idmap: IDMap{
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1000,
						Size:        10000,
					},
				},
			},
			expUID: "",
			expGID: "0:1000:10000",
		},
		{
			idmap: IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1000,
						Size:        10000,
					},
				},
			},
			expUID: "0:1000:10000",
			expGID: "",
		},
		{
			idmap:  IDMap{},
			expUID: "",
			expGID: "",
		},
	} {
		uid, gid := test.idmap.Marshal()
		assert.Equal(t, test.expUID, uid)
		assert.Equal(t, test.expGID, gid)
	}
}

func TestUnmarshal(t *testing.T) {
	for _, test := range []struct {
		idmap  IDMap
		uid    string
		gid    string
		expErr bool
	}{
		{
			idmap: IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1000,
						Size:        10000,
					},
					{
						ContainerID: 1000,
						HostID:      20000,
						Size:        10000,
					},
				},
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1000,
						Size:        1000,
					},
					{
						ContainerID: 1000,
						HostID:      1000,
						Size:        10000,
					},
				},
			},
			uid:    "0:1000:10000,1000:20000:10000",
			gid:    "0:1000:1000,1000:1000:10000",
			expErr: false,
		},
		{
			idmap: IDMap{
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1000,
						Size:        10000,
					},
				},
			},
			uid:    "",
			gid:    "0:1000:10000",
			expErr: false,
		},
		{
			idmap: IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      1000,
						Size:        10000,
					},
				},
			},
			uid:    "0:1000:10000",
			gid:    "",
			expErr: false,
		},
		{
			idmap:  IDMap{},
			uid:    "0-0-0",
			gid:    "0-0-0",
			expErr: true,
		},
		{
			idmap:  IDMap{},
			uid:    "-1:1000:10000",
			gid:    "",
			expErr: true,
		},
		{
			idmap:  IDMap{},
			uid:    "0:1000:",
			gid:    "",
			expErr: true,
		},
		{
			idmap:  IDMap{},
			uid:    "0:1000",
			gid:    "",
			expErr: true,
		},
		{
			idmap:  IDMap{},
			uid:    "1:4294967295:10000",
			gid:    "",
			expErr: true,
		},
		{
			idmap:  IDMap{},
			uid:    "",
			gid:    "1:1:4294967295",
			expErr: true,
		},
		{
			idmap:  IDMap{},
			uid:    "",
			gid:    "",
			expErr: false,
		},
	} {
		var idmap IDMap
		err := idmap.Unmarshal(test.uid, test.gid)
		assert.Equal(t, test.expErr, err != nil)
		assert.Equal(t, test.idmap, idmap)
	}
}
