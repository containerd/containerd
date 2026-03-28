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

package fsview

import (
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
)

// overlayOpaqueXattrs are the xattr names used to indicate an opaque directory.
// "trusted.overlay.opaque" is the traditional xattr used by overlay.
// "user.overlay.opaque" is available since Linux 5.11.
// See https://github.com/torvalds/linux/commit/2d2f2d7322ff43e0fe92bf8cccdc0b09449bf2e1
var overlayOpaqueXattrs = []string{
	"trusted.overlay.opaque",
	"user.overlay.opaque",
}

// NewOverlayFS returns a new fs.FS that overlays the provided layers.
// The layers should be provided in order from upper to lower.
func NewOverlayFS(layers []fs.FS) (fs.FS, error) {
	return &overlayFS{layers: layers}, nil
}

type overlayFS struct {
	layers []fs.FS
}

// hasOpaqueParent checks if any parent directory of the given path is opaque in the layer.
// If a parent is opaque, it means we should not look in lower layers for this path.
func hasOpaqueParent(layer fs.FS, name string) bool {
	// Check all parent directories
	p := name
	for p != "." && p != "/" {
		p = path.Dir(p)
		f, err := layer.Open(p)
		if err != nil {
			continue
		}
		opaque := isOpaque(f)
		f.Close()
		if opaque {
			return true
		}
	}
	return false
}

func (o *overlayFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}

	var dirLayers []fs.FS
	var firstErr error
	var opaque bool

	for _, layer := range o.layers {
		if opaque {
			// A parent directory in a higher layer is opaque, so we stop looking in lower layers
			break
		}
		if hasOpaqueParent(layer, name) {
			// Set opaqueness but continue to check this layer
			opaque = true
		}

		f, err := layer.Open(name)
		if err != nil {
			// Path errors (not found, not a directory, etc.) are expected
			// when a path doesn't resolve in a given layer. Only record
			// non-path errors (e.g., I/O failures) for later reporting.
			var pe *fs.PathError
			if !errors.As(err, &pe) && firstErr == nil {
				firstErr = err
			}
			continue
		}

		fi, errStat := f.Stat()
		if errStat != nil {
			f.Close()
			if firstErr == nil {
				firstErr = errStat
			}
			continue
		}

		if isWhiteout(fi) {
			f.Close()
			// A whiteout hides this path in all lower layers.
			// If we already found directories above, stop merging.
			if len(dirLayers) > 0 {
				break
			}
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}

		if !fi.IsDir() {
			if len(dirLayers) > 0 {
				// Directory on upper covers file on lower
				f.Close()
				break
			}
			return f, nil
		}

		// Directory — accumulate for merging
		dirLayers = append(dirLayers, layer)
		if !opaque && isOpaque(f) {
			opaque = true
		}
		f.Close()
	}

	if len(dirLayers) > 0 {
		return &overlayDir{fs: o, path: name, layers: dirLayers}, nil
	}

	if firstErr != nil {
		return nil, firstErr
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

type overlayDir struct {
	fs      *overlayFS
	path    string
	layers  []fs.FS
	entries []fs.DirEntry
	offset  int
	read    bool
}

func (d *overlayDir) Stat() (fs.FileInfo, error) {
	// Stat should return info from the top-most layer
	if len(d.layers) == 0 {
		return nil, fs.ErrNotExist
	}
	f, err := d.layers[0].Open(d.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

func (d *overlayDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.path, Err: errors.New("is a directory")}
}

func (d *overlayDir) Close() error {
	return nil
}

func (d *overlayDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if !d.read {
		seen := make(map[string]bool)

		for _, layer := range d.layers {
			// ReadDir from the layer
			entries, err := fs.ReadDir(layer, d.path)
			if err == nil {
				for _, e := range entries {
					name := e.Name()
					if seen[name] {
						continue
					}

					// Check for whiteout in this layer
					if (e.Type() & fs.ModeCharDevice) != 0 {
						info, err := e.Info()
						if err == nil && isWhiteout(info) {
							seen[name] = true
							continue
						}
					}

					seen[name] = true
					d.entries = append(d.entries, e)
				}
			}
			// Opaque check is done during Open to filter layers, so we don't need to check here?
			// Wait, Open filters layers for *this* directory.
			// But subdirectories?
			// No, d.layers are the layers for *this* directory.
			// The opaque check in Open ensured that if a layer was opaque, we stopped adding lower layers to d.layers.
			// So we can safely merge all d.layers.
		}
		sort.Slice(d.entries, func(i, j int) bool { return d.entries[i].Name() < d.entries[j].Name() })
		d.read = true
	}

	if n <= 0 {
		if d.offset >= len(d.entries) {
			return []fs.DirEntry{}, nil
		}
		res := d.entries[d.offset:]
		d.offset = len(d.entries)
		return res, nil
	}

	if d.offset >= len(d.entries) {
		return nil, io.EOF
	}

	end := min(d.offset+n, len(d.entries))
	res := d.entries[d.offset:end]
	d.offset = end
	return res, nil
}
