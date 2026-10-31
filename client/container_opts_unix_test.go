//go:build !windows

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

package client

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/containerd/containerd/v2/internal/userns"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/opencontainers/runtime-spec/specs-go"
)

func TestChownHardlinks(t *testing.T) {
	testutil.RequiresRoot(t)
	root := t.TempDir()
	first := filepath.Join(root, "a")
	second := filepath.Join(root, "b")
	if err := os.WriteFile(first, []byte("hard-linked file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Fatal(err)
	}
	// Exercise special-mode preservation as well as ownership remapping.
	if err := os.Chmod(first, 0700|os.ModeSetuid|os.ModeSetgid|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	mapping := []specs.LinuxIDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}}
	if err := filepath.Walk(root, chown(root, userns.IDMap{UidMap: mapping, GidMap: mapping})); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, first, second} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if stat.Uid != 100000 || stat.Gid != 100000 {
			t.Errorf("%s: ownership = %d:%d, want 100000:100000", path, stat.Uid, stat.Gid)
		}
		if path != root {
			want := os.FileMode(0700) | os.ModeSetuid | os.ModeSetgid | os.ModeSticky
			if info.Mode() != want {
				t.Errorf("%s: mode = %v, want %v", path, info.Mode(), want)
			}
		}
	}
}
