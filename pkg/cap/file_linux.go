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

package cap

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Linux UAPI: include/uapi/linux/capability.h.
const (
	revisionMask = 0xff000000
	revision1    = 0x01000000
	revision2    = 0x02000000
	revision3    = 0x03000000
	effective    = 1
	size1        = 12
	size2        = 20
	size3        = 24
)

// FileCapabilities is the content of a security.capability extended attribute.
// Revisions 1 and 2 implicitly scope capabilities to root ID zero.
// Capability masks and flags are preserved verbatim; only RootID is editable.
type FileCapabilities struct {
	RootID uint32
	data   [size3]byte
}

// Namespaced reports whether the parsed attribute explicitly specified a root ID.
func (c FileCapabilities) Namespaced() bool {
	return binary.LittleEndian.Uint32(c.data[:])&revisionMask == revision3
}

// ParseFileCapabilities decodes a revision 1, 2, or 3 file capability attribute.
func ParseFileCapabilities(data []byte) (FileCapabilities, error) {
	var c FileCapabilities
	if len(data) < 4 {
		return c, fmt.Errorf("invalid file capability length %d", len(data))
	}
	magic := binary.LittleEndian.Uint32(data)
	revision := magic & revisionMask
	var size int
	switch revision {
	case revision1:
		size = size1
	case revision2:
		size = size2
	case revision3:
		size = size3
	default:
		return c, fmt.Errorf("unsupported file capability revision %#x", revision)
	}
	if len(data) != size || magic & ^uint32(revisionMask|effective) != 0 {
		return c, errors.New("invalid file capability encoding")
	}
	copy(c.data[:], data)
	if revision == revision3 {
		c.RootID = binary.LittleEndian.Uint32(data[size2:size3])
	}
	return c, nil
}

// Encode returns a revision 3 attribute with an explicit RootID.
// The caller is responsible for translating RootID into the target namespace.
func (c FileCapabilities) Encode() []byte {
	data := c.data
	magic := binary.LittleEndian.Uint32(data[:])
	binary.LittleEndian.PutUint32(data[:], revision3|(magic&effective))
	binary.LittleEndian.PutUint32(data[size2:size3], c.RootID)
	return data[:]
}
