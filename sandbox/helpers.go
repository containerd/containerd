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

package sandbox

import (
	"github.com/containerd/containerd/api/types"
)

// ToProto will map Sandbox struct to it's protobuf definition
func ToProto(s *Sandbox) types.Sandbox {
	return types.Sandbox{
		SandboxID: s.ID,
		Runtime: types.Sandbox_Runtime{
			Name:    s.Runtime.Name,
			Options: s.Runtime.Options,
		},
		Labels:     s.Labels,
		CreatedAt:  s.CreatedAt,
		UpdatedAt:  s.UpdatedAt,
		Extensions: s.Extensions,
		Spec:       s.Spec,
	}
}

// FromProto map protobuf sandbox definition to Sandbox struct
func FromProto(p *types.Sandbox) Sandbox {
	runtime := RuntimeOpts{
		Name:    p.Runtime.Name,
		Options: p.Runtime.Options,
	}

	return Sandbox{
		ID:         p.SandboxID,
		Labels:     p.Labels,
		Runtime:    runtime,
		CreatedAt:  p.CreatedAt,
		UpdatedAt:  p.UpdatedAt,
		Extensions: p.Extensions,
	}
}
