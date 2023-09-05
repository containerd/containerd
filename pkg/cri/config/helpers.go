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
	"fmt"

	runhcsoptions "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/options"
	runtimeoptions "github.com/containerd/containerd/pkg/runtimeoptions/v1"
	"github.com/containerd/containerd/plugin"
	runcoptions "github.com/containerd/containerd/runtime/v2/runc/options"
	"github.com/pelletier/go-toml"
)

const (
	// runtimeRunhcsV1 is the runtime type for runhcs.
	runtimeRunhcsV1 = "io.containerd.runhcs.v1"
)

// GenerateRuntimeOptions generates runtime options from cri plugin config.
func GenerateRuntimeOptions(r Runtime) (interface{}, error) {
	if r.Options == nil {
		return nil, nil
	}
	optionsTree, err := toml.TreeFromMap(r.Options)
	if err != nil {
		return nil, err
	}
	options := getRuntimeOptionsType(r.Type)
	if err := optionsTree.Unmarshal(options); err != nil {
		return nil, err
	}

	// For generic configuration, if no config path specified (preserving old behavior), pass
	// the whole TOML configuration section to the runtime.
	if runtimeOpts, ok := options.(*runtimeoptions.Options); ok && runtimeOpts.ConfigPath == "" {
		runtimeOpts.ConfigBody, err = optionsTree.Marshal()
		if err != nil {
			return nil, fmt.Errorf("failed to marshal TOML blob for runtime %q: %v", r.Type, err)
		}
	}

	return options, nil
}

// getRuntimeOptionsType gets empty runtime options by the runtime type name.
func getRuntimeOptionsType(t string) interface{} {
	switch t {
	case plugin.RuntimeRuncV2:
		return &runcoptions.Options{}
	case runtimeRunhcsV1:
		return &runhcsoptions.Options{}
	default:
		return &runtimeoptions.Options{}
	}
}
