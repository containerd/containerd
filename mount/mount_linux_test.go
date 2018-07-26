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

package mount

import (
	"reflect"
	"testing"
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
