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

package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/containerd/log/logtest"

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

	mh, err := MkdirHandler(root)
	if err != nil {
		t.Fatal(err)
	}

	m := mount.Mount{
		Type:   "mkdir",
		Source: sourcedir,
		Options: []string{
			fmt.Sprintf("mode=%o", testmode),
			fmt.Sprintf("uid=%d", luid),
			fmt.Sprintf("gid=%d", lgid),
		},
	}

	_, err = mh.Mount(ctx, m, "", nil)
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
		t.Fatalf("expected mode 0775 got %o", fi.Mode().Perm())
	}
	sys := fi.Sys().(*syscall.Stat_t)
	if int(sys.Uid) != luid {
		t.Fatalf("expected uid 1000 got %d", sys.Uid)
	}
	if int(sys.Gid) != lgid {
		t.Fatalf("expected gid 1000 got %d", sys.Gid)
	}

	m.Source = filepath.Join(td, "notinroot")
	_, err = mh.Mount(ctx, m, "", nil)
	if err == nil {
		t.Fatal("expected error on source not in root")
	} else if !errdefs.IsNotImplemented(err) {
		t.Fatal(err)
	}
}
