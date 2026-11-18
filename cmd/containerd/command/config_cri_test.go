//go:build !no_cri

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

package command

import (
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/containerd/containerd/v2/version"
)

func TestMigrateConfigCRI(t *testing.T) {
	out := migrateConfigFile(t, `version = 2

[plugins."io.containerd.grpc.v1.cri".cni]
  bin_dir = "/home/kubernetes/bin"
  conf_dir = "/etc/cni/net.d"
  conf_template = ""
`)

	assert.ElementsMatch(t, []string{"version", "plugins"}, slices.Collect(maps.Keys(out)))
	assert.Equal(t, int64(version.ConfigVersion), out["version"])
	// An empty value explicitly set in a plugin config is kept
	assert.Equal(t, map[string]any{
		"bin_dirs":      []any{"/home/kubernetes/bin"},
		"conf_dir":      "/etc/cni/net.d",
		"conf_template": "",
	}, lookup(t, out, "plugins", "io.containerd.cri.v1.runtime", "cni"))
}
