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
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/containerd/log/logtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

func TestMkdirHandler(t *testing.T) {
	ctx := logtest.WithT(context.Background(), t)
	ctx = namespaces.WithNamespace(ctx, "test")
	td := t.TempDir()

	luid := os.Getuid()
	lgid := os.Getgid()
	testmode := os.FileMode(0751)

	root := filepath.Join(td, "root")
	if err := os.MkdirAll(root, 0775); err != nil {
		t.Fatal(err)
	}
	sourcedir := filepath.Join(root, "m")

	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	mh := mkdir{
		rootMap: map[string]*os.Root{
			root: r,
		},
	}

	m := mount.Mount{
		Type:   "mkdir/overlay",
		Source: "overlay",
		Options: []string{
			fmt.Sprintf("X-containerd.mkdir.path=%s:%o:%d:%d", sourcedir, testmode, luid, lgid),
		},
	}

	_, err = mh.Transform(ctx, m, nil)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(sourcedir)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		t.Fatalf("expected directory")
	}
	if fi.Mode().Perm() != testmode {
		t.Fatalf("expected mode %04o got %04o", testmode, fi.Mode().Perm())
	}
	sys := fi.Sys().(*syscall.Stat_t)
	if int(sys.Uid) != luid {
		t.Fatalf("expected uid %d got %d", luid, sys.Uid)
	}
	if int(sys.Gid) != lgid {
		t.Fatalf("expected gid %d got %d", lgid, sys.Gid)
	}

	m.Options = append(m.Options, fmt.Sprintf("X-containerd.mkdir.path=%s", filepath.Join(td, "notinroot")))
	_, err = mh.Transform(ctx, m, nil)
	if err == nil {
		t.Fatal("expected error on source not in root")
	} else if !errdefs.IsNotImplemented(err) {
		t.Fatal(err)
	}
}

// TestMkdirHandlerTargetNotADirectory verifies that mkdir reports a
// clear error when its target already exists but is not a directory,
// rather than accepting it because its permission bits happen to
// match: a regular file with the same mode mkdir would have used is
// exactly the case a mode-only check cannot tell apart from an
// already-created directory.
func TestMkdirHandlerTargetNotADirectory(t *testing.T) {
	ctx := logtest.WithT(context.Background(), t)
	ctx = namespaces.WithNamespace(ctx, "test")
	td := t.TempDir()

	root := filepath.Join(td, "root")
	require.NoError(t, os.MkdirAll(root, 0775))

	testmode := os.FileMode(0751)
	target := filepath.Join(root, "notadir")
	require.NoError(t, os.WriteFile(target, nil, testmode))

	r, err := os.OpenRoot(root)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })
	mh := mkdir{rootMap: map[string]*os.Root{root: r}}

	m := mount.Mount{
		Type:   "mkdir/overlay",
		Source: "overlay",
		Options: []string{
			fmt.Sprintf("X-containerd.mkdir.path=%s:%o", target, testmode),
		},
	}

	_, err = mh.Transform(ctx, m, nil)
	require.Error(t, err)
	assert.True(t, errdefs.IsFailedPrecondition(err), "expected ErrFailedPrecondition, got %v", err)
}
