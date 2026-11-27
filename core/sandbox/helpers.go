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
	"github.com/containerd/containerd/v2/pkg/protobuf"
	gogo_types "github.com/containerd/containerd/v2/pkg/protobuf/types"
	"github.com/containerd/typeurl/v2"
)

// ToProto will map Sandbox struct to it's protobuf definition
func ToProto(sandbox *Sandbox) *types.Sandbox {
	extensions := make(map[string]*gogo_types.Any)
	for k, v := range sandbox.Extensions {
		extensions[k] = typeurl.MarshalProto(v)
	}
	return &types.Sandbox{
		SandboxID: sandbox.ID,
		Runtime: &types.Sandbox_Runtime{
			Name:    sandbox.Runtime.Name,
			Options: typeurl.MarshalProto(sandbox.Runtime.Options),
		},
		Sandboxer:  sandbox.Sandboxer,
		Labels:     sandbox.Labels,
		CreatedAt:  protobuf.ToTimestamp(sandbox.CreatedAt),
		UpdatedAt:  protobuf.ToTimestamp(sandbox.UpdatedAt),
		Extensions: extensions,
		Spec:       typeurl.MarshalProto(sandbox.Spec),
	}
}

// FromProto map protobuf sandbox definition to Sandbox struct
func FromProto(sandboxpb *types.Sandbox) Sandbox {
	runtime := RuntimeOpts{
		Name:    sandboxpb.Runtime.Name,
		Options: sandboxpb.Runtime.Options,
	}

	extensions := make(map[string]typeurl.Any)
	for k, v := range sandboxpb.Extensions {
		extensions[k] = v
	}

	return Sandbox{
		ID:         sandboxpb.SandboxID,
		Labels:     sandboxpb.Labels,
		Runtime:    runtime,
		Spec:       sandboxpb.Spec,
		Sandboxer:  sandboxpb.Sandboxer,
		CreatedAt:  protobuf.FromTimestamp(sandboxpb.CreatedAt),
		UpdatedAt:  protobuf.FromTimestamp(sandboxpb.UpdatedAt),
		Extensions: extensions,
	}
}

// CheckpointContainersToProto maps checkpoint containers to their protobuf
// definition.
func CheckpointContainersToProto(containers []CheckpointContainer) []*types.CheckpointSandboxContainer {
	if len(containers) == 0 {
		return nil
	}
	out := make([]*types.CheckpointSandboxContainer, 0, len(containers))
	for _, c := range containers {
		out = append(out, &types.CheckpointSandboxContainer{ID: c.ID, Name: c.Name})
	}
	return out
}

// CheckpointContainersFromProto maps protobuf checkpoint containers to
// CheckpointContainer structs.
func CheckpointContainersFromProto(containers []*types.CheckpointSandboxContainer) []CheckpointContainer {
	if len(containers) == 0 {
		return nil
	}
	out := make([]CheckpointContainer, 0, len(containers))
	for _, c := range containers {
		out = append(out, CheckpointContainer{ID: c.GetID(), Name: c.GetName()})
	}
	return out
}

// RestoreContainersToProto maps restore containers to their protobuf
// definition.
func RestoreContainersToProto(containers []RestoreContainer) []*types.RestoreSandboxContainer {
	if len(containers) == 0 {
		return nil
	}
	out := make([]*types.RestoreSandboxContainer, 0, len(containers))
	for _, c := range containers {
		out = append(out, &types.RestoreSandboxContainer{
			ID:   c.ID,
			Name: c.Name,
			Spec: typeurl.MarshalProto(c.Spec),
		})
	}
	return out
}

// RestoreContainersFromProto maps protobuf restore containers to
// RestoreContainer structs.
func RestoreContainersFromProto(containers []*types.RestoreSandboxContainer) []RestoreContainer {
	if len(containers) == 0 {
		return nil
	}
	out := make([]RestoreContainer, 0, len(containers))
	for _, c := range containers {
		rc := RestoreContainer{ID: c.GetID(), Name: c.GetName()}
		// Assigning a nil *anypb.Any would make Spec a non-nil interface.
		if c.GetSpec() != nil {
			rc.Spec = c.GetSpec()
		}
		out = append(out, rc)
	}
	return out
}

// ExtensionsToProto maps extensions to their protobuf definition.
func ExtensionsToProto(extensions map[string]typeurl.Any) map[string]*gogo_types.Any {
	if len(extensions) == 0 {
		return nil
	}
	out := make(map[string]*gogo_types.Any, len(extensions))
	for k, v := range extensions {
		out[k] = typeurl.MarshalProto(v)
	}
	return out
}

// ExtensionsFromProto maps protobuf extensions to typeurl.Any values.
func ExtensionsFromProto(extensions map[string]*gogo_types.Any) map[string]typeurl.Any {
	if len(extensions) == 0 {
		return nil
	}
	out := make(map[string]typeurl.Any, len(extensions))
	for k, v := range extensions {
		out[k] = v
	}
	return out
}
