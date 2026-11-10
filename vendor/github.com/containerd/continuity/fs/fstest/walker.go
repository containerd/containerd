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
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// resource represents a filesystem entry for directory comparison.
type resource struct {
	path   string
	paths  []string // all paths, including hardlinks (sorted)
	mode   os.FileMode
	uid    int64
	gid    int64
	size   int64
	sha256 [sha256.Size]byte // regular files only
	target string            // symlinks only
	major  uint64            // devices only
	minor  uint64            // devices only
}

// buildResources walks root and returns a sorted list of resources.
func buildResources(root string) ([]resource, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	type entry struct {
		res resource
		fi  os.FileInfo
	}

	// hlKey -> index into entries for the first file with that inode.
	hardlinks := map[hardlinkKey]int{}
	var entries []entry

	err = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		// Use absolute-style paths like continuity does (rooted at "/").
		rel = "/" + filepath.ToSlash(rel)
		if rel == "/." {
			// skip root directory itself
			return nil
		}

		r := resource{
			path: rel,
			mode: fi.Mode(),
		}
		statResource(fi, &r)

		if fi.Mode().IsRegular() {
			r.size = fi.Size()
			h, err := hashFile(p)
			if err != nil {
				return err
			}
			r.sha256 = h

			// Check for hardlink.
			if key, ok := getHardlinkKey(fi); ok {
				if idx, exists := hardlinks[key]; exists {
					// Merge into existing entry.
					entries[idx].res.paths = append(entries[idx].res.paths, rel)
					return nil
				}
				hardlinks[key] = len(entries)
			}
		} else if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			r.target = target
		} else if fi.Mode()&os.ModeDevice != 0 {
			r.major, r.minor = getDeviceInfo(fi)
		} else if fi.Mode()&os.ModeNamedPipe != 0 {
			// Check for hardlink on named pipes.
			if key, ok := getHardlinkKey(fi); ok {
				if idx, exists := hardlinks[key]; exists {
					entries[idx].res.paths = append(entries[idx].res.paths, rel)
					return nil
				}
				hardlinks[key] = len(entries)
			}
		}

		r.paths = []string{rel}
		entries = append(entries, entry{res: r, fi: fi})
		return nil
	})
	if err != nil {
		return nil, err
	}

	resources := make([]resource, len(entries))
	for i, e := range entries {
		sort.Strings(e.res.paths)
		e.res.path = e.res.paths[0]
		resources[i] = e.res
	}
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].path < resources[j].path
	})

	return resources, nil
}

func hashFile(path string) ([sha256.Size]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [sha256.Size]byte{}, err
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}
