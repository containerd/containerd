//go:build linux

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

package apparmordelivery

import (
	delivery "github.com/containerd/containerd/v2/pkg/apparmordelivery"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

func init() {
	registry.Register(&plugin.Registration{
		Type:   plugins.ServicePlugin,
		ID:     "apparmordelivery",
		Config: &delivery.Config{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			cfg := ic.Config.(*delivery.Config)
			service, err := delivery.NewService(cfg)
			if err != nil {
				return nil, err
			}
			ic.Meta.Capabilities = []string{"apparmor"}
			return service, nil
		},
	})
}
