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

package diff

import (
	"context"
	"fmt"
	"slices"

	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/services"
	"github.com/containerd/containerd/v2/version"
)

var defaultDifferConfig = &config{
	Order:  []string{"walking", "overlay"},
	SyncFs: false,
}

func configMigration(ctx context.Context, configVersion int, pluginConfigs map[string]any) error {
	if configVersion >= version.ConfigVersion {
		return nil
	}
	name := fmt.Sprintf("%s.%s", string(plugins.ServicePlugin), services.DiffService)
	original, ok := pluginConfigs[name]
	if !ok {
		return nil
	}
	src := original.(map[string]any)
	dst := map[string]any{}

	for k, v := range src {
		switch k {
		case "default":
			dst[k] = migrateOrder(v.([]any))
		default:
			dst[k] = v
		}
	}

	pluginConfigs[name] = dst
	return nil
}

func migrateOrder(old []any) []any {
	if slices.Contains(old, "overlay") {
		// If overlay is already in the order, keep the order as is.
		return old
	}
	var new []any
	for _, v := range old {
		new = append(new, v)
		if v == "walking" {
			new = append(new, "overlay")
		}
	}
	return new
}
