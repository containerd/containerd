// +build linux

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
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"testing"

	"github.com/containerd/aufs"
	"github.com/containerd/containerd/archive/tartest"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/containerd/snapshots/overlay"
	"github.com/containerd/continuity/fs/fstest"
)

func runWriterToTarTest(t *testing.T, name string, wt tartest.WriterToTar, a fstest.Applier, validate func(string) error, applyErr error) {
	t.Run(name, makeWriterToTarNaiveTest(wt, a, validate, applyErr))
	t.Run(name+"Overlay", makeWriterToTarOverlayTest(wt, a, validate, applyErr))
	t.Run(name+"Aufs", makeWriterToTarAufsTest(wt, a, validate, applyErr))
}

func makeWriterToTarOverlayTest(wt tartest.WriterToTar, a fstest.Applier, validate func(string) error, applyErr error) func(*testing.T) {
	return func(t *testing.T) {
		testutil.RequiresRoot(t)
		if err := overlay.Supported("/"); err != nil {
			t.Skipf("skipping because overlay is not supported %v", err)
		}
		ud, err := ioutil.TempDir("", "test-writer-to-tar-overlay-upper")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(ud)
		wd, err := ioutil.TempDir("", "test-writer-to-tar-overlay-work")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(wd)

		apply := func(ctx context.Context, _ string, tr io.Reader) error {
			_, err := Apply(ctx, ud, tr, WithConvertWhiteout(OverlayConvertWhiteout))
			return err
		}

		validate := func(td string) error {
			mounts := []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						fmt.Sprintf("workdir=%s", wd),
						fmt.Sprintf("upperdir=%s", ud),
						fmt.Sprintf("lowerdir=%s", td),
					},
				},
			}

			return mount.WithTempMount(context.Background(), mounts, func(root string) error {
				return validate(root)
			})
		}

		makeWriterToTarTest(wt, a, apply, validate, applyErr)(t)
	}
}

func makeWriterToTarAufsTest(wt tartest.WriterToTar, a fstest.Applier, validate func(string) error, applyErr error) func(*testing.T) {
	return func(t *testing.T) {
		testutil.RequiresRoot(t)
		if err := aufs.Supported(); err != nil {
			t.Skipf("skipping because aufs is not supported %v", err)
		}
		d, err := ioutil.TempDir("", "test-writer-to-tar-aufs-rw")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(d)

		apply := func(ctx context.Context, _ string, tr io.Reader) error {
			_, err := Apply(ctx, d, tr, WithConvertWhiteout(AufsConvertWhiteout))
			return err
		}

		validate := func(td string) error {
			mounts := []mount.Mount{
				{
					Type:   "aufs",
					Source: "none",
					Options: []string{
						fmt.Sprintf("br:%s=rw:%s=ro+wh", d, td),
					},
				},
			}

			return mount.WithTempMount(context.Background(), mounts, func(root string) error {
				return validate(root)
			})
		}

		makeWriterToTarTest(wt, a, apply, validate, applyErr)(t)
	}
}
