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
	"fmt"

	"github.com/containerd/log"

	"github.com/containerd/containerd/v2/core/mount"
)

const (
	// define mount options using X-containerd prefix as defined by
	// https://man7.org/linux/man-pages/man8/mount.8.html

	prefixMkdir = "X-containerd.mkdir."
	prefixMkfs  = "X-containerd.mkfs."
)

type typeTransformer struct {
	mount.Transformer

	mountType string
}

// deferredEnsure is an impure side effect deferred until run is
// called. targets are the absolute path(s) run produces.
type deferredEnsure struct {
	targets []string
	run     func(context.Context) error
}

func (t typeTransformer) Transform(ctx context.Context, m mount.Mount, a []mount.ActiveMount) (mount.Mount, error) {
	m.Type = t.mountType
	return t.Transformer.Transform(ctx, m, a)
}

// rewritePosition applies chain, a position's pending transforms, to
// m. mkfs and mkdir return their impure work as a deferredEnsure
// instead of performing it immediately; every other transformer runs
// in full.
func rewritePosition(ctx context.Context, chain []mount.Transformer, m mount.Mount, resolved []mount.ActiveMount) (mount.Mount, []deferredEnsure, error) {
	var ensures []deferredEnsure
	for _, elem := range chain {
		tt, ok := elem.(typeTransformer)
		if !ok {
			rewritten, err := elem.Transform(ctx, m, resolved)
			if err != nil {
				return mount.Mount{}, nil, err
			}
			m = rewritten
			continue
		}
		m.Type = tt.mountType
		switch tr := tt.Transformer.(type) {
		case mountFormatter:
			rewritten, err := tr.Transform(ctx, m, resolved)
			if err != nil {
				return mount.Mount{}, nil, err
			}
			m = rewritten
		case *mkfs:
			rewritten, ensure, err := tr.rewrite(m)
			if err != nil {
				return mount.Mount{}, nil, err
			}
			m = rewritten
			if ensure.run != nil {
				ensures = append(ensures, ensure)
			}
		case *mkdir:
			rewritten, ensure, err := tr.rewrite(m)
			if err != nil {
				return mount.Mount{}, nil, err
			}
			m = rewritten
			if ensure.run != nil {
				ensures = append(ensures, ensure)
			}
		default:
			log.G(ctx).WithField("transform_type", fmt.Sprintf("%T", tr)).Warn("transform has no pure rewrite, running it in full")
			rewritten, err := tt.Transform(ctx, m, resolved)
			if err != nil {
				return mount.Mount{}, nil, err
			}
			m = rewritten
		}
	}
	return m, ensures, nil
}
