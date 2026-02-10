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
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"text/template"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/errdefs"
	"github.com/erofs/go-erofs"
)

// View is an interface for temporarily viewing a filesystem,
// implementing the fs.FS interface with a close method.
type View interface {
	fs.FS
	Close() error
}

type view struct {
	fs.FS
	cleanup func() error
}

func (v view) Close() error {
	if v.cleanup != nil {
		return v.cleanup()
	}
	return nil
}

// FSMounts will return a fs.FS for the provided mounts if possible to open
// the mounts directly without mounting. Possible mounts for direct open..
// - Bind mounts: Able to just open the source path directly
// - Overlay mounts: Able to open the merged path directly if all lower/upper work dirs are accessible
// - erofs mounts: Able to open the source paths directory using go-erofs library and overlay the results
// - format/* mounts: Apply template formatting using previous mount sources, then process the result
//
// If not supported, a nil fs.FS and an error will be returned.
func FSMounts(m []mount.Mount) (View, error) {
	if len(m) == 0 {
		return nil, nil
	}

	return mountToView(m[len(m)-1], m[:len(m)-1])
}

// mountToView converts a mount to a View, using preceding mounts for resolution if needed.
func mountToView(m mount.Mount, preceding []mount.Mount) (View, error) {
	switch m.Type {
	case "bind", "rbind":
		r, err := os.OpenRoot(m.Source)
		if err != nil {
			return nil, err
		}
		return view{
			FS:      r.FS(),
			cleanup: r.Close,
		}, nil
	case "erofs":
		return openEROFS(m)
	case "overlay":
		return openOverlay(m)
	default:
		// Check if this is a format/* mount
		if strings.HasPrefix(m.Type, "format/") {
			return openFormatMount(m, preceding)
		}
	}

	return nil, fmt.Errorf("mount type %s cannot be directly viewed: %w", m.Type, errdefs.ErrNotImplemented)
}

// openEROFS opens an EROFS mount as a View.
func openEROFS(m mount.Mount) (View, error) {
	f, err := os.Open(m.Source)
	if err != nil {
		return nil, err
	}

	// Check for additional devices in mount options
	var extraDevices []io.ReaderAt
	var closers []io.Closer
	closers = append(closers, f)

	for _, opt := range m.Options {
		if strings.HasPrefix(opt, "devices=") {
			devicePaths := strings.Split(strings.TrimPrefix(opt, "devices="), ",")
			for _, devPath := range devicePaths {
				if devPath == "" {
					continue
				}
				df, err := os.Open(devPath)
				if err != nil {
					for _, c := range closers {
						c.Close()
					}
					return nil, err
				}
				closers = append(closers, df)
				extraDevices = append(extraDevices, df)
			}
		}
	}

	var opts []erofs.Opt
	if len(extraDevices) > 0 {
		opts = append(opts, erofs.WithExtraDevices(extraDevices...))
	}

	efs, err := erofs.EroFS(f, opts...)
	if err != nil {
		for _, c := range closers {
			c.Close()
		}
		return nil, err
	}

	return view{
		FS: efs,
		cleanup: func() error {
			var errs []error
			for _, c := range closers {
				errs = append(errs, c.Close())
			}
			return errors.Join(errs...)
		},
	}, nil
}

// openOverlay opens an overlay mount as a View.
func openOverlay(m mount.Mount) (View, error) {
	layers, err := getOverlayFSLayers(m.Options)
	if err != nil {
		return nil, err
	}

	// Extract fs.FS from each View
	var fsList []fs.FS
	for _, layer := range layers {
		fsList = append(fsList, layer)
	}

	ofs, err := NewOverlayFS(fsList)
	if err != nil {
		// Cleanup all layer views
		for _, layer := range layers {
			layer.Close()
		}
		return nil, err
	}

	return view{
		FS: ofs,
		cleanup: func() error {
			var errs []error
			for _, layer := range layers {
				errs = append(errs, layer.Close())
			}
			return errors.Join(errs...)
		},
	}, nil
}

// openFormatMount opens a format/* mount by resolving templates and creating an overlay.
func openFormatMount(m mount.Mount, preceding []mount.Mount) (View, error) {
	types := strings.Split(m.Type, "/")
	if len(types) < 2 || types[0] != "format" || types[len(types)-1] != "overlay" {
		return nil, errdefs.ErrNotImplemented
	}

	var layers []View
	for _, opt := range m.Options {
		if strings.HasPrefix(opt, "upperdir=") {
			upper, err := handleOverlayFormat(strings.TrimPrefix(opt, "upperdir="), preceding)
			if err != nil {
				if errors.Is(err, errdefs.ErrNotImplemented) {
					// Do no include upper if not locally viewable, likely ephemeral and empty
					continue
				}
				return nil, fmt.Errorf("failed to handle upperdir option: %w", err)
			}
			// Extract the upperdir value and format it
			if len(layers) > 0 {
				layers = append(upper, layers...)
			} else {
				layers = upper
			}
		}
		if strings.HasPrefix(opt, "lowerdir=") {
			for _, l := range strings.Split(strings.TrimPrefix(opt, "lowerdir="), ":") {
				lowers, err := handleOverlayFormat(l, preceding)
				if err != nil {
					return nil, fmt.Errorf("failed to handle lowerdir option: %w", err)
				}
				layers = append(layers, lowers...)
			}
		}
	}

	// Extract fs.FS from each View
	var fsList []fs.FS
	for _, layer := range layers {
		fsList = append(fsList, layer)
	}

	ofs, err := NewOverlayFS(fsList)
	if err != nil {
		// Cleanup all layer views
		for _, layer := range layers {
			layer.Close()
		}
		return nil, err
	}

	return view{
		FS: ofs,
		cleanup: func() error {
			var errs []error
			for _, layer := range layers {
				errs = append(errs, layer.Close())
			}
			return errors.Join(errs...)
		},
	}, nil
}

// handleOverlayFormat resolves formatted overlay values
func handleOverlayFormat(s string, preceding []mount.Mount) ([]View, error) {
	if !strings.Contains(s, "{{") {
		// Only directories may be used for overlay
		r, err := os.OpenRoot(s)
		if err != nil {
			return nil, err
		}
		return []View{view{
			FS:      r.FS(),
			cleanup: r.Close,
		}}, nil
	}

	var layers []View
	fm := template.FuncMap{
		"source": func(i int) (string, error) {
			if i < 0 || i >= len(preceding) {
				return "", fmt.Errorf("index out of bounds: %d, has %d preceding mounts", i, len(preceding))
			}
			v, err := mountToView(preceding[i], preceding[:i])
			if err != nil {
				return "", fmt.Errorf("failed to open source of mount %d: %w", i, err)
			}
			layers = append(layers, v)
			return "", nil
		},
		"mount": func(i int) (string, error) {
			if i < 0 || i >= len(preceding) {
				return "", fmt.Errorf("index out of bounds: %d, has %d preceding mounts", i, len(preceding))
			}
			v, err := mountToView(preceding[i], preceding[:i])
			if err != nil {
				return "", fmt.Errorf("failed to open source of mount %d: %w", i, err)
			}
			layers = append(layers, v)

			return "", nil
		},
		"overlay": func(start, end int) (string, error) {
			i := start
			for {
				if i < 0 || i >= len(preceding) {
					return "", fmt.Errorf("index out of bounds: %d, has %d preceding mounts", i, len(preceding))
				}
				v, err := mountToView(preceding[i], preceding[:i])
				if err != nil {
					return "", fmt.Errorf("failed to open source of mount %d: %w", i, err)
				}
				layers = append(layers, v)

				if i == end {
					break
				}
				if start > end {
					i--
				} else {
					i++
				}
			}
			return "", nil
		},
	}

	t, err := template.New("").Funcs(fm).Parse(s)
	if err != nil {
		return nil, err
	}

	if err := t.Execute(io.Discard, nil); err != nil {
		for _, l := range layers {
			l.Close()
		}
		return nil, err
	}
	return layers, nil
}

func getOverlayFSLayers(options []string) ([]View, error) {
	var (
		lower string
		paths []string
	)
	for _, o := range options {
		if strings.HasPrefix(o, "lowerdir=") {
			lower = strings.TrimPrefix(o, "lowerdir=")
		} else if strings.HasPrefix(o, "upperdir=") {
			paths = append(paths, strings.TrimPrefix(o, "upperdir="))
		}
	}

	if lower != "" {
		lowers := strings.Split(lower, ":")
		paths = append(paths, lowers...)
	}

	var layers []View
	for _, p := range paths {
		layer, err := openPath(p)
		if err != nil {
			// Cleanup already opened layers
			for _, l := range layers {
				l.Close()
			}
			return nil, err
		}
		layers = append(layers, layer)
	}

	return layers, nil
}

// openPath opens a filesystem path and returns a View.
// It tries to detect if the path is an EROFS file or a directory.
func openPath(path string) (View, error) {
	// Check if path is a file or directory
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		// Open as directory
		root, err := os.OpenRoot(path)
		if err != nil {
			return nil, err
		}
		return view{
			FS:      root.FS(),
			cleanup: root.Close,
		}, nil
	}

	// Try to open as EROFS file
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	efs, err := erofs.EroFS(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to open %s as EROFS: %w", path, err)
	}

	return view{
		FS:      efs,
		cleanup: f.Close,
	}, nil
}
