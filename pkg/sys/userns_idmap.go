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
	"fmt"
	"math"

	"github.com/opencontainers/runtime-spec/specs-go"
)

var (
	badIdentify = Identity{UID: math.MaxUint32, GID: math.MaxUint32}
)

// Identity is a UID and GID pair of a user
type Identity struct {
	UID uint32
	GID uint32
}

// IdentityMapping contains a mappings of UIDs and GIDs.
type IdentityMapping struct {
	UIDMaps []specs.LinuxIDMapping `json:"UIDMaps"`
	GIDMaps []specs.LinuxIDMapping `json:"GIDMaps"`
}

// RootID returns the ID pair for the root user
func (i IdentityMapping) RootPair() (Identity, error) {
	uid, err := toHost(0, i.UIDMaps)
	if err != nil {
		return badIdentify, err
	}
	gid, err := toHost(0, i.GIDMaps)
	if err != nil {
		return badIdentify, err
	}
	return Identity{UID: uid, GID: gid}, nil
}

// ToHost returns the host Identity pair for the container uid, gid.
func (i IdentityMapping) ToHost(pair Identity) (Identity, error) {
	var (
		target Identity
		err    error
	)
	target.UID, err = toHost(pair.UID, i.UIDMaps)
	if err != nil {
		return badIdentify, err
	}
	target.GID, err = toHost(pair.GID, i.GIDMaps)
	if err != nil {
		return badIdentify, err
	}
	return target, nil
}

// ToContainer returns the container Identify pair for the host uid and gid
func (i IdentityMapping) ToContainer(pair Identity) (Identity, error) {
	var (
		target Identity
		err    error
	)
	target.UID, err = toContainer(pair.UID, i.UIDMaps)
	if err != nil {
		return badIdentify, err
	}
	target.GID, err = toContainer(pair.GID, i.GIDMaps)
	if err != nil {
		return badIdentify, err
	}
	return target, nil
}

// Empty returns true if there are no id mappings
func (i IdentityMapping) Empty() bool {
	return len(i.UIDMaps) == 0 && len(i.GIDMaps) == 0
}

// toContainer takes an id mapping, and uses it to translate a
// host ID to the remapped ID. If no map is provided, then the translation
// assumes a 1-to-1 mapping and returns the passed in id
func toContainer(hostID uint32, idMap []specs.LinuxIDMapping) (uint32, error) {
	if idMap == nil {
		return hostID, nil
	}
	for _, m := range idMap {
		if (hostID >= m.HostID) && (hostID <= (m.HostID + m.Size - 1)) {
			contID := m.ContainerID + (hostID - m.HostID)
			return contID, nil
		}
	}
	return math.MaxUint32, fmt.Errorf("Host ID %d cannot be mapped to a container ID", hostID)
}

// toHost takes an id mapping and a remapped ID, and translates the
// ID to the mapped host ID. If no map is provided, then the translation
// assumes a 1-to-1 mapping and returns the passed in id #
func toHost(contID uint32, idMap []specs.LinuxIDMapping) (uint32, error) {
	if idMap == nil {
		return contID, nil
	}
	for _, m := range idMap {
		if (contID >= m.ContainerID) && (contID <= (m.ContainerID + m.Size - 1)) {
			hostID := m.HostID + (contID - m.ContainerID)
			return hostID, nil
		}
	}
	return math.MaxUint32, fmt.Errorf("Container ID %d cannot be mapped to a host ID", contID)
}
