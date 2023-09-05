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

import (
	"context"
	"fmt"

	runcoptions "github.com/containerd/containerd/runtime/v2/runc/options"
)

// validatePlatformSpecific validates the platform specific options
func validatePlatformSpecific(ctx context.Context, c *PluginConfig) error {
	var systemdCgroup bool
	var systemdCgroupSettingFound bool

	for k, r := range c.ContainerdConfig.Runtimes {
		opts, err := GenerateRuntimeOptions(r)
		if err != nil {
			return fmt.Errorf("failed to parse runtime options of %q: %w", k, err)
		}

		if isSystemd, ok := runtimeHandlerHasSystemdCgroup(opts); ok {
			if systemdCgroupSettingFound && isSystemd != systemdCgroup {
				return fmt.Errorf("conflicting SystemdCgroup settings found in runtime handler options, all runc runtime configs expected to have the same setting for SystemdCgroup")
			}
			systemdCgroup = isSystemd
			systemdCgroupSettingFound = true
		}
	}
	return nil
}

func runtimeHandlerHasSystemdCgroup(opts interface{}) (bool, bool) {
	switch v := opts.(type) {
	case *runcoptions.Options:
		systemdCgroup := v.SystemdCgroup
		if systemdCgroup {
			return true, true
		}
		return false, true
	}
	return false, false
}
