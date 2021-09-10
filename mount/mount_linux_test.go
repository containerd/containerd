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
	td, err := os.MkdirTemp("", "fuse")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(td); err != nil {
			t.Fatal(err)
		}
	}()

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
