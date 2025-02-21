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

package integrityverifier

import (
	"github.com/containerd/containerd/v2/pkg/integrity/fsverity"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

// TODO: see if 'root' directory value can be derived from elsewhere
const defaultIntegrityPath = "/var/lib/containerd/io.containerd.content.v1.content/integrity/"

func init() {
	registry.Register(&plugin.Registration{
		Type:   plugins.IntegrityVerifierPlugin,
		ID:     "fsverity",
		Config: &fsverity.Config{StorePath: defaultIntegrityPath},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			cfg := ic.Config.(*fsverity.Config)
			return fsverity.NewValidator(*cfg), nil
		},
	})
}
