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

package archive

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/containerd/containerd/log/logtest"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/containerd/snapshots/overlay/overlayutils"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/continuity/fs/fstest"
	"github.com/pkg/errors"
)

func TestOverlayApply(t *testing.T) {
	testutil.RequiresRoot(t)

	base, err := os.MkdirTemp("", "test-ovl-diff-apply-")
	if err != nil {
		t.Fatalf("unable to create temp dir: %+v", err)
	}
	defer os.RemoveAll(base)

	if err := overlayutils.Supported(base); err != nil {
		t.Skipf("skipping because overlay is not supported %v", err)
	}
	fstest.FSSuite(t, overlayDiffApplier{
		tmp:  base,
		diff: WriteDiff,
		t:    t,
	})
}

func TestOverlayApplyNoParents(t *testing.T) {
	testutil.RequiresRoot(t)

	base, err := os.MkdirTemp("", "test-ovl-diff-apply-")
	if err != nil {
		t.Fatalf("unable to create temp dir: %+v", err)
	}
	defer os.RemoveAll(base)

	if err := overlayutils.Supported(base); err != nil {
		t.Skipf("skipping because overlay is not supported %v", err)
	}
	fstest.FSSuite(t, overlayDiffApplier{
		tmp: base,
		diff: func(ctx context.Context, w io.Writer, a, b string, _ ...WriteDiffOpt) error {
			cw := NewChangeWriter(w, b)
			cw.addedDirs = nil
			err := fs.Changes(ctx, a, b, cw.HandleChange)
			if err != nil {
				return errors.Wrap(err, "failed to create diff tar stream")
			}
			return cw.Close()
		},
		t: t,
	})
}

type overlayDiffApplier struct {
	tmp  string
	diff func(context.Context, io.Writer, string, string, ...WriteDiffOpt) error
	t    *testing.T
}

type overlayContext struct {
	merged  string
	lowers  []string
	mounted bool
}

type contextKey struct{}

func (d overlayDiffApplier) TestContext(ctx context.Context) (context.Context, func(), error) {
	merged, err := os.MkdirTemp(d.tmp, "merged")
	if err != nil {
		return ctx, nil, errors.Wrap(err, "failed to make merged dir")
	}

	oc := &overlayContext{
		merged: merged,
	}

	ctx = logtest.WithT(ctx, d.t)

	return context.WithValue(ctx, contextKey{}, oc), func() {
		if oc.mounted {
			mount.Unmount(oc.merged, 0)
		}
	}, nil
}

func (d overlayDiffApplier) Apply(ctx context.Context, a fstest.Applier) (string, func(), error) {
	oc := ctx.Value(contextKey{}).(*overlayContext)

	applyCopy, err := os.MkdirTemp(d.tmp, "apply-copy-")
	if err != nil {
		return "", nil, errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(applyCopy)

	base := oc.merged
	if len(oc.lowers) == 1 {
		base = oc.lowers[0]
	}

	if err = fs.CopyDir(applyCopy, base); err != nil {
		return "", nil, errors.Wrap(err, "failed to copy base")
	}

	if err := a.Apply(applyCopy); err != nil {
		return "", nil, errors.Wrap(err, "failed to apply changes to copy of base")
	}

	buf := bytes.NewBuffer(nil)

	if err := d.diff(ctx, buf, base, applyCopy); err != nil {
		return "", nil, errors.Wrap(err, "failed to create diff")
	}

	if oc.mounted {
		if err := mount.Unmount(oc.merged, 0); err != nil {
			return "", nil, errors.Wrap(err, "failed to unmount")
		}
		oc.mounted = false
	}

	next, err := os.MkdirTemp(d.tmp, "lower-")
	if err != nil {
		return "", nil, errors.Wrap(err, "failed to create temp dir")
	}

	if _, err = Apply(ctx, next, buf, WithConvertWhiteout(OverlayConvertWhiteout), WithParents(oc.lowers)); err != nil {
		return "", nil, errors.Wrap(err, "failed to apply tar stream")
	}

	oc.lowers = append([]string{next}, oc.lowers...)

	if len(oc.lowers) == 1 {
		return oc.lowers[0], nil, nil
	}

	m := mount.Mount{
		Type:   "overlay",
		Source: "overlay",
		Options: []string{
			fmt.Sprintf("lowerdir=%s", strings.Join(oc.lowers, ":")),
		},
	}

	if err := m.Mount(oc.merged); err != nil {
		return "", nil, errors.Wrapf(err, "failed to mount: %v", m)
	}
	oc.mounted = true

	return oc.merged, nil, nil
}
