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

package fstest

import (
	"bytes"
	"fmt"
	"os"
)

type resourceUpdate struct {
	Original resource
	Updated  resource
}

func (u resourceUpdate) String() string {
	return fmt.Sprintf("%s(mode: %o, uid: %d, gid: %d) -> %s(mode: %o, uid: %d, gid: %d)",
		u.Original.path, u.Original.mode, u.Original.uid, u.Original.gid,
		u.Updated.path, u.Updated.mode, u.Updated.uid, u.Updated.gid,
	)
}

type resourceListDifference struct {
	Additions []resource
	Deletions []resource
	Updates   []resourceUpdate
}

func (l resourceListDifference) HasDiff() bool {
	if len(l.Deletions) > 0 || len(l.Updates) > 0 || (len(metadataFiles) == 0 && len(l.Additions) > 0) {
		return true
	}

	for _, add := range l.Additions {
		if ok := metadataFiles[add.path]; !ok {
			return true
		}
	}

	return false
}

func (l resourceListDifference) String() string {
	buf := bytes.NewBuffer(nil)
	for _, add := range l.Additions {
		fmt.Fprintf(buf, "+ %s\n", add.path)
	}
	for _, del := range l.Deletions {
		fmt.Fprintf(buf, "- %s\n", del.path)
	}
	for _, upt := range l.Updates {
		fmt.Fprintf(buf, "~ %s\n", upt.String())
	}
	return buf.String()
}

// diffResourceList compares two resource lists and returns the list
// of adds updates and deletes, resource lists are not reordered
// before doing difference.
func diffResourceList(r1, r2 []resource) resourceListDifference {
	i1 := 0
	i2 := 0
	var d resourceListDifference

	for i1 < len(r1) && i2 < len(r2) {
		p1 := r1[i1].path
		p2 := r2[i2].path
		switch {
		case p1 < p2:
			d.Deletions = append(d.Deletions, r1[i1])
			i1++
		case p1 == p2:
			if !compareResource(r1[i1], r2[i2]) {
				d.Updates = append(d.Updates, resourceUpdate{
					Original: r1[i1],
					Updated:  r2[i2],
				})
			}
			i1++
			i2++
		case p1 > p2:
			d.Additions = append(d.Additions, r2[i2])
			i2++
		}
	}

	for i1 < len(r1) {
		d.Deletions = append(d.Deletions, r1[i1])
		i1++

	}
	for i2 < len(r2) {
		d.Additions = append(d.Additions, r2[i2])
		i2++
	}

	return d
}

func compareResource(r1, r2 resource) bool {
	if r1.path != r2.path {
		return false
	}
	if r1.mode != r2.mode {
		return false
	}
	if r1.uid != r2.uid {
		return false
	}
	if r1.gid != r2.gid {
		return false
	}

	return compareResourceType(r1, r2)
}

func compareResourceType(r1, r2 resource) bool {
	mode := r1.mode
	switch {
	case mode.IsRegular():
		if r1.size != r2.size {
			return false
		}
		if r1.sha256 != r2.sha256 {
			return false
		}
		if len(r1.paths) != len(r2.paths) {
			return false
		}
		for i := range r1.paths {
			if r1.paths[i] != r2.paths[i] {
				return false
			}
		}
		return true
	case mode.IsDir():
		return true
	case mode&os.ModeSymlink != 0:
		return r1.target == r2.target
	case mode&os.ModeNamedPipe != 0:
		return true
	case mode&os.ModeDevice != 0:
		return r1.major == r2.major && r1.minor == r2.minor
	default:
		return true
	}
}
