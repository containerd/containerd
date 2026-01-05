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

package types

import (
	"github.com/containerd/go-cni"
	"github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

// SandboxInfo is extra information for sandbox.
// TODO (mikebrow): discuss predefining constants structures for some or all of these field names in CRI
type SandboxInfo struct {
	Pid            uint32                    `json:"pid"`
	Status         string                    `json:"processStatus"`
	NetNSClosed    bool                      `json:"netNamespaceClosed"`
	Image          string                    `json:"image"`
	SnapshotKey    string                    `json:"snapshotKey"`
	Snapshotter    string                    `json:"snapshotter"`
	RuntimeType    string                    `json:"runtimeType"`
	RuntimeOptions interface{}               `json:"runtimeOptions"`
	Config         *runtime.PodSandboxConfig `json:"config"`
	// Note: RuntimeSpec may not be populated if the sandbox has not been fully created.
	RuntimeSpec *specs.Spec       `json:"runtimeSpec"`
	CNIResult   *cni.Result       `json:"cniResult"`
	Metadata    *sandbox.Metadata `json:"sandboxMetadata"`
}
