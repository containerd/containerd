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

package plugin

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// TestConfigDecodesMkfsOptions verifies that the mkfs.erofs settings decode
// without unknown keys, including mkfs_options from earlier releases.
func TestConfigDecodesMkfsOptions(t *testing.T) {
	const cfg = `
use_mkfs = true
mkfs_options = ["-T0", "--mkfs-time"]
enable_tar_index = true
`
	var c Config
	if err := toml.NewDecoder(strings.NewReader(cfg)).DisallowUnknownFields().Decode(&c); err != nil {
		t.Fatalf("decode config with mkfs_options: %v", err)
	}
	if !c.UseMkfs || len(c.MkfsOptions) != 2 || !c.EnableTarIndex {
		t.Errorf("config decoded as %+v; want use_mkfs, mkfs_options and enable_tar_index set", c)
	}
}

// TestConfigUseMkfs verifies that mkfs_options selects mkfs.erofs without
// use_mkfs, so configurations from releases that always used mkfs.erofs keep
// their behavior.
func TestConfigUseMkfs(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    Config
		want bool
	}{
		{name: "default", want: false},
		{name: "use_mkfs", c: Config{UseMkfs: true}, want: true},
		{name: "mkfs_options", c: Config{MkfsOptions: []string{"-zlz4"}}, want: true},
		{name: "empty mkfs_options", c: Config{MkfsOptions: []string{}}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.useMkfs(); got != tc.want {
				t.Errorf("useMkfs() = %v, want %v", got, tc.want)
			}
		})
	}
}
