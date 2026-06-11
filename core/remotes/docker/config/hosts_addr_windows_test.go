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

package config

import "testing"

func TestNativeUnixAddrWindows(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			// Drive-letter path: the URL form is /C:/... (matches file://
			// convention); strip the leading "/" and turn forward slashes
			// into backslashes for the Windows filesystem.
			name: "uppercase drive",
			in:   "/C:/ProgramData/registry-cache/reg.sock",
			want: `C:\ProgramData\registry-cache\reg.sock`,
		},
		{
			// Drive letters are case-insensitive on Windows; accept lowercase.
			name: "lowercase drive",
			in:   "/c:/foo/bar.sock",
			want: `c:\foo\bar.sock`,
		},
		{
			name: "non-C drive",
			in:   "/D:/data/reg.sock",
			want: `D:\data\reg.sock`,
		},
		{
			name: "shortest drive path",
			in:   "/C:/a",
			want: `C:\a`,
		},
		{
			// No drive letter: leading "/" stays. Windows treats a path like
			// "\foo\bar" as relative to the current drive root, so this is
			// still meaningful even though it's an unusual configuration.
			name: "drive-less absolute",
			in:   "/run/foo.sock",
			want: `\run\foo.sock`,
		},
		{
			// Letter followed by a non-colon is NOT a drive letter; the path
			// is treated as drive-less.
			name: "letter without colon",
			in:   "/abc/foo",
			want: `\abc\foo`,
		},
		{
			// Abstract names start with "@", no leading "/" and no drive
			// letter; pass through filepath.FromSlash unchanged.
			name: "abstract",
			in:   "@registry-cache",
			want: "@registry-cache",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nativeUnixAddr(tc.in)
			if got != tc.want {
				t.Fatalf("nativeUnixAddr(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
