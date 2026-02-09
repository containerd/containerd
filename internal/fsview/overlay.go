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
		if err == nil {
			opaque := isOpaque(f)
			f.Close()
			if opaque {
				return true
			}
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
		if err == nil {
			// Check if whiteout
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
				// If we have accumulated directories, this whiteout hides lower layers for this path.
				// Since we are at the same path, if we found a directory above, we wouldn't see a whiteout here?
				// Actually, if upper is dir, we look for lower. Lower could be a whiteout?
				// No, whiteout 0,0 is a character device file.
				// If Upper is Dir, Lower is CharDev(0,0).
				// Dir covers CharDev. So we shouldn't care if lower is whiteout if we already found a dir?
				// Standard overlay: Whiteout hides things in *lower* layers.
				// If we found a file/dir in Upper, we stop and return it.
				// If we found a Dir in Upper, we want to merge with Lower Dir.
				// If Lower is Whiteout, then Upper Dir "replaces" it? Or is it an opaque dir?
				// "A whiteout marks a file or directory as deleted in lower layers."
				// If I have Dir A in Upper, and Whiteout A in Lower1.
				// Does A exist? Yes, as a directory. Does it merge with Lower2?
				// Logic: Whiteout at Layer N hides Layer N+1...
				// So if we found Dir at Layer N-1, does Whiteout at Layer N stop us looking at N+1?
				// Yes.
				if len(dirLayers) > 0 {
					break
				}
				return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
			}

			if fi.IsDir() {
				dirLayers = append(dirLayers, layer)
				// Check opaque
				if !opaque && isOpaque(f) {
					opaque = true
				}
				f.Close()
			} else {
				// File found
				if len(dirLayers) > 0 {
					// Directory on upper covers file on lower
					f.Close()
					break
				}
				return f, nil
			}
		} else {
			if !errors.Is(err, fs.ErrNotExist) {
				if firstErr == nil {
					firstErr = err
				}
			}
		}
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

	end := d.offset + n
	if end > len(d.entries) {
		end = len(d.entries)
	}
	res := d.entries[d.offset:end]
	d.offset = end
	return res, nil
}
