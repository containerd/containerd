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

package container

import (
	"encoding/json"
	"fmt"

	"github.com/containerd/containerd/v2/pkg/protobuf/proto"
	"github.com/containerd/log"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// NOTE(random-liu):
// 1) Metadata is immutable after created.
// 2) Metadata is checkpointed as containerd container label.

// metadataVersion1 is legacy version of container metadata.
const metadataVersion1 = "v1"

// metadataVersion2 is current version of container metadata.
const metadataVersion2 = "v2"

// versionedMetadata is the internal versioned container metadata.
type versionedMetadata struct {
	// Version indicates the version of the versioned container metadata.
	Version string
	// Metadata's type is metadataInternal. If not there will be a recursive call in MarshalJSON.
	Metadata metadataInternal
}

// metadataInternal is for internal use.
type metadataInternal struct {
	ID           string
	Name         string
	SandboxID    string
	Config       json.RawMessage
	ImageRef     string
	LogPath      string
	StopSignal   string
	ProcessLabel string
}

// Metadata is the unversioned container metadata.
type Metadata struct {
	// ID is the container id.
	ID string
	// Name is the container name.
	Name string
	// SandboxID is the sandbox id the container belongs to.
	SandboxID string
	// Config is the CRI container config.
	// NOTE(random-liu): Resource limits are updatable, the source
	// of truth for resource limits are in containerd.
	Config *runtime.ContainerConfig
	// ImageRef is the reference of image used by the container.
	ImageRef string
	// LogPath is the container log path.
	LogPath string
	// StopSignal is the system call signal that will be sent to the container to exit.
	// TODO(random-liu): Add integration test for stop signal.
	StopSignal string
	// ProcessLabel is the SELinux process label for the container
	ProcessLabel string
}

// MarshalJSON encodes Metadata into bytes in json format.
func (c *Metadata) MarshalJSON() ([]byte, error) {
	m, err := metadataToInternal(*c, metadataVersion2)
	if err != nil {
		return nil, err
	}
	return json.Marshal(&versionedMetadata{
		Version:  metadataVersion2,
		Metadata: m,
	})
}

// UnmarshalJSON decodes Metadata from bytes.
func (c *Metadata) UnmarshalJSON(data []byte) error {
	versioned := &versionedMetadata{}
	if err := json.Unmarshal(data, versioned); err != nil {
		return err
	}
	// Handle old version after upgrade.
	switch versioned.Version {
	case metadataVersion1, metadataVersion2:
		*c = internalToMetadata(versioned.Metadata, versioned.Version)
		return nil
	}

	return fmt.Errorf("unsupported version: %q", versioned.Version)
}

// TODO: remove support for legacy v1 config, which marshals runtime.ContainerConfig as JSON.
// Marshaling protobuf messages as JSON is fragile and does not handle struct changes well.
// v2 encodes runtime.ContainerConfig using native proto.Marshal/proto.Unmarshal, stored internally as []byte.

func internalToMetadata(c metadataInternal, version string) Metadata {
	// Config is intentionally left nil if it cannot be unmarshaled to trigger cleanup of containers with invalid ContainerConfig.
	// Returning the error from Unmarshal prevents the container from being visible to the CRI container store, triggering creation of duplicate replacements.
	// See: https://github.com/containerd/containerd/pull/13453#issuecomment-4755099592

	m := Metadata{
		ID:           c.ID,
		Name:         c.Name,
		SandboxID:    c.SandboxID,
		ImageRef:     c.ImageRef,
		LogPath:      c.LogPath,
		StopSignal:   c.StopSignal,
		ProcessLabel: c.ProcessLabel,
	}

	config := &runtime.ContainerConfig{}
	switch version {
	case metadataVersion1:
		if err := json.Unmarshal(c.Config, config); err != nil {
			log.L.WithError(err).WithField("container", c.ID).Errorf("Failed to unmarshal %s container metadata config", version)
			return m
		}
	case metadataVersion2:
		b := []byte{}
		if err := json.Unmarshal(c.Config, &b); err != nil {
			log.L.WithError(err).WithField("container", c.ID).Errorf("Failed to unmarshal %s container metadata config as JSON", version)
			return m
		}
		if b == nil {
			return m
		}
		if err := proto.Unmarshal(b, config); err != nil {
			log.L.WithError(err).WithField("container", c.ID).Errorf("Failed to unmarshal %s container metadata config as protobuf", version)
			return m
		}
	default:
		log.L.WithError(fmt.Errorf("unsupported version: %q", version)).WithField("container", c.ID).Error("Failed to unmarshal container metadata config")
		return m
	}

	m.Config = config
	return m
}

func metadataToInternal(c Metadata, version string) (metadataInternal, error) {
	m := metadataInternal{
		ID:           c.ID,
		Name:         c.Name,
		SandboxID:    c.SandboxID,
		ImageRef:     c.ImageRef,
		LogPath:      c.LogPath,
		StopSignal:   c.StopSignal,
		ProcessLabel: c.ProcessLabel,
	}

	var config []byte
	var err error
	switch version {
	case metadataVersion1:
		// v1 stores ContainerConfig as JSON
		config, err = json.Marshal(c.Config)
		if err != nil {
			log.L.WithError(err).WithField("container", c.ID).Errorf("Failed to marshal %s container metadata config", version)
			return m, err
		}
	case metadataVersion2:
		// v2 stores ContainerConfig marshalled as json []byte, holding marshalled protobuf
		if c.Config != nil {
			config, err = proto.Marshal(c.Config)
			if err != nil {
				log.L.WithError(err).WithField("container", c.ID).Errorf("Failed to marshal %s container metadata config as protobuf", version)
				return m, err
			}
		}
		config, err = json.Marshal(config)
		if err != nil {
			log.L.WithError(err).WithField("container", c.ID).Errorf("Failed to marshal %s container metadata config as JSON", version)
			return m, err
		}
	default:
		err = fmt.Errorf("unsupported version: %q", version)
		log.L.WithError(err).WithField("container", c.ID).Error("Failed to marshal container metadata config")
		return m, err
	}

	m.Config = config
	return m, nil
}
