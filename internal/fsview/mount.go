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
	"io/fs"
	"os"
	"strings"
	"text/template/parse"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/errdefs"
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

// readLinkView wraps an fs.ReadLinkFS with a cleanup function,
// implementing both View and fs.ReadLinkFS.
type readLinkView struct {
	fs.ReadLinkFS
	cleanup func() error
}

func (v readLinkView) Close() error {
	if v.cleanup != nil {
		return v.cleanup()
	}
	return nil
}

// newView creates a View that preserves fs.ReadLinkFS if the underlying
// fs.FS implements it.
func newView(fsys fs.FS, cleanup func() error) View {
	if rl, ok := fsys.(fs.ReadLinkFS); ok {
		return readLinkView{ReadLinkFS: rl, cleanup: cleanup}
	}
	return view{FS: fsys, cleanup: cleanup}
}

// FSMounts returns a View for the provided mounts if possible to open
// the mounts directly without mounting.
//
// If not supported, a nil View and an error will be returned.
func FSMounts(m []mount.Mount) (View, error) {
	if len(m) == 0 {
		return nil, nil
	}
	return resolveMount(m[len(m)-1], m[:len(m)-1])
}

// resolveMount tries registered handlers first, then built-in handlers.
func resolveMount(m mount.Mount, preceding []mount.Mount) (View, error) {
	for _, h := range registered {
		if h.HandleMount == nil {
			continue
		}
		v, err := h.HandleMount(m)
		if errors.Is(err, errdefs.ErrNotImplemented) {
			continue
		}
		return v, err
	}

	switch {
	case m.Type == "bind" || m.Type == "rbind":
		return openBind(m)
	case m.Type == "overlay":
		return openOverlay(m)
	case strings.HasPrefix(m.Type, "format/"):
		return openFormatMount(m, preceding)
	}

	return nil, fmt.Errorf("mount type %s cannot be directly viewed: %w", m.Type, errdefs.ErrNotImplemented)
}

func openBind(m mount.Mount) (View, error) {
	r, err := os.OpenRoot(m.Source)
	if err != nil {
		return nil, err
	}
	return newView(r.FS(), r.Close), nil
}

func openOverlay(m mount.Mount) (View, error) {
	layers, err := openOverlayPaths(m.Options)
	if err != nil {
		return nil, err
	}
	return newOverlayView(layers)
}

func openFormatMount(m mount.Mount, preceding []mount.Mount) (View, error) {
	types := strings.Split(m.Type, "/")
	if len(types) < 2 || types[0] != "format" || types[len(types)-1] != "overlay" {
		return nil, errdefs.ErrNotImplemented
	}

	var layers []View
	closeLayers := func() {
		for _, l := range layers {
			l.Close()
		}
	}
	for _, opt := range m.Options {
		if val, ok := strings.CutPrefix(opt, "upperdir="); ok {
			upper, err := resolveOverlayValue(val, preceding)
			if err != nil {
				if errors.Is(err, errdefs.ErrNotImplemented) {
					continue
				}
				closeLayers()
				return nil, fmt.Errorf("failed to handle upperdir option: %w", err)
			}
			if len(layers) > 0 {
				layers = append(upper, layers...)
			} else {
				layers = upper
			}
		}
		if val, ok := strings.CutPrefix(opt, "lowerdir="); ok {
			for l := range strings.SplitSeq(val, ":") {
				lowers, err := resolveOverlayValue(l, preceding)
				if err != nil {
					closeLayers()
					return nil, fmt.Errorf("failed to handle lowerdir option: %w", err)
				}
				layers = append(layers, lowers...)
			}
		}
	}

	return newOverlayView(layers)
}

func newOverlayView(layers []View) (View, error) {
	var fsList []fs.FS
	for _, layer := range layers {
		fsList = append(fsList, layer)
	}

	ofs, err := NewOverlayFS(fsList)
	if err != nil {
		for _, layer := range layers {
			layer.Close()
		}
		return nil, err
	}

	return newView(ofs, func() error {
		var errs []error
		for _, layer := range layers {
			errs = append(errs, layer.Close())
		}
		return errors.Join(errs...)
	}), nil
}

// resolveOverlayValue resolves a single overlay option value, which may be
// a plain directory path or a Go template expression like "{{ mount 0 }}".
func resolveOverlayValue(s string, preceding []mount.Mount) ([]View, error) {
	if !strings.Contains(s, "{{") {
		r, err := os.OpenRoot(s)
		if err != nil {
			return nil, err
		}
		return []View{newView(r.FS(), r.Close)}, nil
	}

	tmplExpr, suffix := splitTemplateSuffix(s)

	var layers []View
	addLayer := func(v View) { layers = append(layers, v) }
	closeLayers := func() {
		for _, l := range layers {
			_ = l.Close()
		}
	}
	boundsCheck := func(i int) error {
		if i < 0 || i >= len(preceding) {
			return fmt.Errorf("index out of bounds: %d, has %d preceding mounts", i, len(preceding))
		}
		return nil
	}

	// These functions resolve mount references as side effects. Their return
	// values are only needed to satisfy the template function signatures.
	fm := map[string]any{
		"source": func(i int) (string, error) {
			if err := boundsCheck(i); err != nil {
				return "", err
			}
			r, err := os.OpenRoot(preceding[i].Source)
			if err != nil {
				return "", fmt.Errorf("failed to open source of mount %d: %w", i, err)
			}
			addLayer(newView(r.FS(), r.Close))
			return "", nil
		},
		"mount": func(i int) (string, error) {
			if err := boundsCheck(i); err != nil {
				return "", err
			}
			v, err := resolveMount(preceding[i], preceding[:i])
			if err != nil {
				return "", fmt.Errorf("failed to resolve mount %d: %w", i, err)
			}
			addLayer(v)
			return "", nil
		},
		"overlay": func(start, end int) (string, error) {
			i := start
			for {
				if err := boundsCheck(i); err != nil {
					return "", err
				}
				v, err := resolveMount(preceding[i], preceding[:i])
				if err != nil {
					return "", fmt.Errorf("failed to resolve mount %d: %w", i, err)
				}
				addLayer(v)
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

	trees, err := parse.Parse("mount", tmplExpr, "{{", "}}", fm)
	if err != nil {
		return nil, err
	}

	if err := resolveTemplateActions(trees["mount"].Root, func(name string, args []int) error {
		switch name {
		case "source":
			_, err := fm["source"].(func(int) (string, error))(args[0])
			return err
		case "mount":
			_, err := fm["mount"].(func(int) (string, error))(args[0])
			return err
		case "overlay":
			_, err := fm["overlay"].(func(int, int) (string, error))(args[0], args[1])
			return err
		}
		return fmt.Errorf("unsupported mount template function %q: %w", name, errdefs.ErrNotImplemented)
	}); err != nil {
		closeLayers()
		return nil, err
	}

	if suffix != "" {
		for i, l := range layers {
			subFS, err := fs.Sub(l, suffix)
			if err != nil {
				closeLayers()
				return nil, fmt.Errorf("failed to create sub view for path %q: %w", suffix, err)
			}
			layers[i] = newView(subFS, l.Close)
		}
	}

	return layers, nil
}

// resolveTemplateActions evaluates mount-reference actions in a parsed template.
// Only direct calls to source, mount, and overlay with integer arguments are
// supported. Text is ignored, as the original template output was discarded.
func resolveTemplateActions(root *parse.ListNode, resolve func(string, []int) error) error {
	if root == nil {
		return nil
	}

	for _, node := range root.Nodes {
		switch n := node.(type) {
		case *parse.TextNode, *parse.CommentNode:
			continue

		case *parse.ActionNode:
			if n.Pipe == nil || len(n.Pipe.Decl) != 0 || len(n.Pipe.Cmds) != 1 {
				return fmt.Errorf("unsupported mount template action %q: %w", n.String(), errdefs.ErrNotImplemented)
			}

			args := n.Pipe.Cmds[0].Args
			if len(args) == 0 {
				return fmt.Errorf("empty mount template action: %w", errdefs.ErrNotImplemented)
			}

			ident, ok := args[0].(*parse.IdentifierNode)
			if !ok {
				return fmt.Errorf("unsupported mount template action %q: %w", n.String(), errdefs.ErrNotImplemented)
			}

			var want int
			switch ident.Ident {
			case "source", "mount":
				want = 1
			case "overlay":
				want = 2
			default:
				return fmt.Errorf("unsupported mount template function %q: %w", ident.Ident, errdefs.ErrNotImplemented)
			}

			if len(args)-1 != want {
				return fmt.Errorf("invalid arguments for %s: expected %d, got %d", ident.Ident, want, len(args)-1)
			}

			indices := make([]int, 0, want)
			for _, arg := range args[1:] {
				num, ok := arg.(*parse.NumberNode)
				if !ok || !num.IsInt {
					return fmt.Errorf("unsupported mount template argument %q: %w", arg.String(), errdefs.ErrNotImplemented)
				}

				i := int(num.Int64)
				if int64(i) != num.Int64 {
					return fmt.Errorf("mount index %q overflows int", num.Text)
				}
				indices = append(indices, i)
			}

			if err := resolve(ident.Ident, indices); err != nil {
				return err
			}

		default:
			return fmt.Errorf("unsupported mount template node %T: %w", node, errdefs.ErrNotImplemented)
		}
	}

	return nil
}

func splitTemplateSuffix(s string) (string, string) {
	i := strings.LastIndex(s, "}}")
	if i < 0 {
		return s, ""
	}
	tmpl := s[:i+2]
	suffix := strings.TrimPrefix(s[i+2:], "/")
	return tmpl, suffix
}

func openOverlayPaths(options []string) ([]View, error) {
	var (
		lower string
		paths []string
	)
	for _, o := range options {
		if val, ok := strings.CutPrefix(o, "lowerdir="); ok {
			lower = val
		} else if val, ok := strings.CutPrefix(o, "upperdir="); ok {
			paths = append(paths, val)
		}
	}
	if lower != "" {
		paths = append(paths, strings.Split(lower, ":")...)
	}

	var layers []View
	for _, p := range paths {
		r, err := os.OpenRoot(p)
		if err != nil {
			for _, l := range layers {
				l.Close()
			}
			return nil, err
		}
		layers = append(layers, newView(r.FS(), r.Close))
	}
	return layers, nil
}
