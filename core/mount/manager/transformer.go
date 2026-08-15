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

func (t typeTransformer) Transform(ctx context.Context, m mount.Mount, a []mount.ActiveMount) (mount.Mount, error) {
	m.Type = t.mountType
	return t.Transformer.Transform(ctx, m, a)
}

// rewritePosition applies chain, a position's pending transforms as
// recorded by the loop which builds mountConv, to m. mountFormatter is
// already pure, so it is applied directly; mkfs and mkdir also have a
// pure part, computing the mount value their options imply, and an
// impure part which actually creates whatever that implies. Only the
// pure part runs here: the impure part is returned as a closure for
// the caller to run whenever it, not this function, decides reality
// needs to change, which lets this run inside a bolt transaction to
// resolve a mount's final identity without that transaction ever
// waiting on real filesystem work.
//
// A transformer this package does not know how to split this way is
// run in full immediately as a defensive fallback; none of the
// transforms this package registers reach that branch.
func rewritePosition(ctx context.Context, chain []mount.Transformer, m mount.Mount, resolved []mount.ActiveMount) (mount.Mount, []func(context.Context) error, error) {
	var ensures []func(context.Context) error
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
			if ensure != nil {
				ensures = append(ensures, ensure)
			}
		case *mkdir:
			rewritten, ensure, err := tr.rewrite(m)
			if err != nil {
				return mount.Mount{}, nil, err
			}
			m = rewritten
			if ensure != nil {
				ensures = append(ensures, ensure)
			}
		default:
			log.G(ctx).Warnf("transform %T has no pure rewrite, running it in full", tr)
			rewritten, err := tt.Transform(ctx, m, resolved)
			if err != nil {
				return mount.Mount{}, nil, err
			}
			m = rewritten
		}
	}
	return m, ensures, nil
}
