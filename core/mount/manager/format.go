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

package manager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/template/parse"

	"github.com/containerd/containerd/v2/core/mount"
)

const formatCheck = "{{"

type mountFormatter struct{}

func (mountFormatter) Transform(_ context.Context, m mount.Mount, a []mount.ActiveMount) (mount.Mount, error) {
	if sc := formatString(m.Source); sc != nil {
		f, err := sc(a)
		if err != nil {
			return m, err
		}
		m.Source = f
	}

	if tc := formatString(m.Target); tc != nil {
		f, err := tc(a)
		if err != nil {
			return m, err
		}
		m.Target = f
	}

	var o []string
	for i := range m.Options {
		if oc := formatString(m.Options[i]); oc != nil {
			f, err := oc(a)
			if err != nil {
				return m, err
			}
			if o == nil {
				o = make([]string, len(m.Options))
				copy(o, m.Options)
			}
			o[i] = f
		}
	}
	if o != nil {
		m.Options = o
	}
	return m, nil
}

func formatString(s string) func([]mount.ActiveMount) (string, error) {
	if !strings.Contains(s, formatCheck) {
		return nil
	}

	return func(a []mount.ActiveMount) (string, error) {
		fm := map[string]any{
			"source": func(i int) (string, error) {
				if i < 0 || i >= len(a) {
					return "", fmt.Errorf("index out of bounds: %d, has %d active mounts", i, len(a))
				}
				return a[i].Source, nil
			},
			"target": func(i int) (string, error) {
				if i < 0 || i >= len(a) {
					return "", fmt.Errorf("index out of bounds: %d, has %d active mounts", i, len(a))
				}
				return a[i].Target, nil
			},
			"mount": func(i int) (string, error) {
				if i < 0 || i >= len(a) {
					return "", fmt.Errorf("index out of bounds: %d, has %d active mounts", i, len(a))
				}
				return a[i].MountPoint, nil
			},
			"overlay": func(start, end int) (string, error) {
				var dirs []string
				if start > end {
					if start >= len(a) || end < 0 {
						return "", fmt.Errorf("invalid range: %d-%d, has %d active mounts", start, end, len(a))
					}
					for i := start; i >= end; i-- {
						dirs = append(dirs, a[i].MountPoint)
					}
				} else {
					if start < 0 || end >= len(a) {
						return "", fmt.Errorf("invalid range: %d-%d, has %d active mounts", start, end, len(a))
					}
					for i := start; i <= end; i++ {
						dirs = append(dirs, a[i].MountPoint)
					}
				}
				return strings.Join(dirs, ":"), nil
			},
		}

		trees, err := parse.Parse("mount", s, "{{", "}}", fm)
		if err != nil {
			return "", err
		}
		return executeMountTemplate(trees["mount"].Root, fm)
	}
}

// executeMountTemplate evaluates mount-reference expressions without
// using the full Go template execution engine.
func executeMountTemplate(root *parse.ListNode, funcs map[string]any) (string, error) {
	var buf strings.Builder

	for _, node := range root.Nodes {
		switch n := node.(type) {
		case *parse.TextNode:
			buf.Write(n.Text)

		case *parse.ActionNode:
			if n.Pipe == nil || len(n.Pipe.Decl) != 0 || len(n.Pipe.Cmds) != 1 {
				return "", fmt.Errorf("unsupported mount template action %q", n.String())
			}

			args := n.Pipe.Cmds[0].Args
			if len(args) == 0 {
				return "", errors.New("empty mount template action")
			}

			ident, ok := args[0].(*parse.IdentifierNode)
			if !ok {
				return "", fmt.Errorf("unsupported mount template action %q", n.String())
			}

			// Only literal integer arguments are supported.
			indices := make([]int, 0, len(args)-1)
			for _, arg := range args[1:] {
				num, ok := arg.(*parse.NumberNode)
				if !ok || !num.IsInt {
					return "", fmt.Errorf("invalid argument %q for %s", arg.String(), ident.Ident)
				}

				i := int(num.Int64)
				if int64(i) != num.Int64 {
					return "", fmt.Errorf("mount index %q overflows int", num.Text)
				}
				indices = append(indices, i)
			}

			var (
				value string
				err   error
			)

			switch ident.Ident {
			case "source", "target", "mount":
				if len(indices) != 1 {
					return "", fmt.Errorf("%s expects 1 argument, got %d", ident.Ident, len(indices))
				}
				fn := funcs[ident.Ident].(func(int) (string, error))
				value, err = fn(indices[0])

			case "overlay":
				if len(indices) != 2 {
					return "", fmt.Errorf("overlay expects 2 arguments, got %d", len(indices))
				}
				fn := funcs["overlay"].(func(int, int) (string, error))
				value, err = fn(indices[0], indices[1])

			default:
				return "", fmt.Errorf("unsupported mount template function %q", ident.Ident)
			}

			if err != nil {
				return "", err
			}
			buf.WriteString(value)

		default:
			return "", fmt.Errorf("unsupported mount template node %T", node)
		}
	}

	return buf.String(), nil
}
