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

package imageverifier

import (
	"time"

	"github.com/containerd/containerd/pkg/imageverifier/bindir"
	"github.com/containerd/containerd/plugin"
)

// Register default image verifier service plugin
func init() {
	plugin.Register(&plugin.Registration{
		Type:   plugin.ImageVerifierPlugin,
		ID:     "bindir",
		Config: defaultConfig(),
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			cfg := ic.Config.(*bindir.Config)
			return bindir.NewImageVerifier(cfg), nil
		},
	})
}

func defaultConfig() *bindir.Config {
	return &bindir.Config{
		BinDir:             defaultPath,
		MaxVerifiers:       10,
		PerVerifierTimeout: 10 * time.Second,
	}
}
