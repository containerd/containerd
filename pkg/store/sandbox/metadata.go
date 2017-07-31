/*
Copyright 2017 The Kubernetes Authors.

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
	"encoding/json"
	"fmt"

	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
)

// NOTE(random-liu):
// 1) Metadata is immutable after created.
// 2) Metadata is checkpointed as containerd container label.

// metadataVersion is current version of sandbox metadata.
const metadataVersion = "v1" // nolint

// versionedMetadata is the internal versioned sandbox metadata.
// nolint
type versionedMetadata struct {
	// Version indicates the version of the versioned sandbox metadata.
	Version string
	Metadata
}

// Metadata is the unversioned sandbox metadata.
type Metadata struct {
	// ID is the sandbox id.
	ID string
	// Name is the sandbox name.
	Name string
	// Config is the CRI sandbox config.
	Config *runtime.PodSandboxConfig
	// CreatedAt is the created timestamp.
	// TODO(random-liu): Use containerd container CreatedAt (containerd#933)
	CreatedAt int64
	// Pid is the process id of the sandbox.
	Pid uint32
	// NetNS is the network namespace used by the sandbox.
	NetNS string
}

// Encode encodes Metadata into bytes in json format.
func (c *Metadata) Encode() ([]byte, error) {
	return json.Marshal(&versionedMetadata{
		Version:  metadataVersion,
		Metadata: *c,
	})
}

// Decode decodes Metadata from bytes.
func (c *Metadata) Decode(data []byte) error {
	versioned := &versionedMetadata{}
	if err := json.Unmarshal(data, versioned); err != nil {
		return err
	}
	// Handle old version after upgrade.
	switch versioned.Version {
	case metadataVersion:
		*c = versioned.Metadata
		return nil
	}
	return fmt.Errorf("unsupported version")
}
