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

package mount

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/containerd/continuity/testutil"
	exec "golang.org/x/sys/execabs"
	"golang.org/x/sys/unix"
)

func TestLongestCommonPrefix(t *testing.T) {
	tcases := []struct {
		in       []string
		expected string
	}{
		{[]string{}, ""},
		{[]string{"foo"}, "foo"},
		{[]string{"foo", "bar"}, ""},
		{[]string{"foo", "foo"}, "foo"},
		{[]string{"foo", "foobar"}, "foo"},
		{[]string{"foo", "", "foobar"}, ""},
	}

	for i, tc := range tcases {
		if got := longestCommonPrefix(tc.in); got != tc.expected {
			t.Fatalf("[%d case] expected (%s), but got (%s)", i+1, tc.expected, got)
		}
	}
}

func TestCompactLowerdirOption(t *testing.T) {
	tcases := []struct {
		opts      []string
		commondir string
		newopts   []string
	}{
		// no lowerdir or only one
		{
			[]string{"workdir=a"},
			"",
			[]string{"workdir=a"},
		},
		{
			[]string{"workdir=a", "lowerdir=b"},
			"",
			[]string{"workdir=a", "lowerdir=b"},
		},

		// >= 2 lowerdir
		{
			[]string{"lowerdir=/snapshots/1/fs:/snapshots/10/fs"},
			"/snapshots/",
			[]string{"lowerdir=1/fs:10/fs"},
		},
		{
			[]string{"lowerdir=/snapshots/1/fs:/snapshots/10/fs:/snapshots/2/fs"},
			"/snapshots/",
			[]string{"lowerdir=1/fs:10/fs:2/fs"},
		},

		// if common dir is /
		{
			[]string{"lowerdir=/snapshots/1/fs:/other_snapshots/1/fs"},
			"",
			[]string{"lowerdir=/snapshots/1/fs:/other_snapshots/1/fs"},
		},
	}

	for i, tc := range tcases {
		dir, opts := compactLowerdirOption(tc.opts)
		if dir != tc.commondir {
			t.Fatalf("[%d case] expected common dir (%s), but got (%s)", i+1, tc.commondir, dir)
		}

		if !reflect.DeepEqual(opts, tc.newopts) {
			t.Fatalf("[%d case] expected options (%v), but got (%v)", i+1, tc.newopts, opts)
		}
	}
}

func TestFUSEHelper(t *testing.T) {
	testutil.RequiresRoot(t)
	const fuseoverlayfsBinary = "fuse-overlayfs"
	_, err := exec.LookPath(fuseoverlayfsBinary)
	if err != nil {
		t.Skip("fuse-overlayfs not installed")
	}
	td := t.TempDir()

	for _, dir := range []string{"lower1", "lower2", "upper", "work", "merged"} {
		if err := os.Mkdir(filepath.Join(td, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}

	opts := fmt.Sprintf("lowerdir=%s:%s,upperdir=%s,workdir=%s", filepath.Join(td, "lower2"), filepath.Join(td, "lower1"), filepath.Join(td, "upper"), filepath.Join(td, "work"))
	m := Mount{
		Type:    "fuse3." + fuseoverlayfsBinary,
		Source:  "overlay",
		Options: []string{opts},
	}
	dest := filepath.Join(td, "merged")
	if err := m.Mount(dest); err != nil {
		t.Fatal(err)
	}
	if err := UnmountAll(dest, 0); err != nil {
		t.Fatal(err)
	}
}

func TestMountAt(t *testing.T) {
	testutil.RequiresRoot(t)

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	defer unix.Unmount(filepath.Join(dir2, "bar"), unix.MNT_DETACH)

	if err := os.WriteFile(filepath.Join(dir1, "foo"), []byte("foo"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir2, "bar"), []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	// mount ${dir1}/foo at ${dir2}/bar
	// But since we are using `mountAt` we only need to specify the relative path to dir2 as the target mountAt will chdir to there.
	if err := mountAt(dir2, filepath.Join(dir1, "foo"), "bar", "none", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dir2, "bar"))
	if err != nil {
		t.Fatal(err)
	}

	if string(b) != "foo" {
		t.Fatalf("unexpected file content: %s", b)
	}

	newWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if wd != newWD {
		t.Fatalf("unexpected working directory: %s", newWD)
	}
}

func TestUnmountMounts(t *testing.T) {
	testutil.RequiresRoot(t)

	target, mounts := setupMounts(t)
	if err := UnmountMounts(mounts, target, 0); err != nil {
		t.Fatal(err)
	}
}

func TestUnmountRecursive(t *testing.T) {
	testutil.RequiresRoot(t)

	target, _ := setupMounts(t)
	if err := UnmountRecursive(target, 0); err != nil {
		t.Fatal(err)
	}
}

func setupMounts(t *testing.T) (target string, mounts []Mount) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	if err := os.Mkdir(filepath.Join(dir1, "foo"), 0755); err != nil {
		t.Fatal(err)
	}
	mounts = append(mounts, Mount{
		Type:   "bind",
		Source: dir1,
		Options: []string{
			"ro",
			"rbind",
		},
	})

	if err := os.WriteFile(filepath.Join(dir2, "bar"), []byte("bar"), 0644); err != nil {
		t.Fatal(err)
	}
	mounts = append(mounts, Mount{
		Type:   "bind",
		Source: dir2,
		Target: "foo",
		Options: []string{
			"ro",
			"rbind",
		},
	})

	target = t.TempDir()
	if err := All(mounts, target); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(target, "foo/bar"))
	if err != nil {
		t.Fatal(err)
	}

	if string(b) != "bar" {
		t.Fatalf("unexpected file content: %s", b)
	}

	return target, mounts
}
