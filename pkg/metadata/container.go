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

package metadata

import (
	"encoding/json"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata/store"

	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"
)

// The code is very similar with sandbox.go, but there is no template support
// in golang, we have to have similar files for different types.
// TODO(random-liu): Figure out a way to simplify this.
// TODO(random-liu): Handle versioning with the same mechanism with container.go

// containerMetadataVersion  is current version of container metadata.
const containerMetadataVersion = "v1" // nolint

// versionedContainerMetadata is the internal versioned container metadata.
// nolint
type versionedContainerMetadata struct {
	// Version indicates the version of the versioned container metadata.
	Version string
	ContainerMetadata
}

// ContainerMetadata is the unversioned container metadata.
type ContainerMetadata struct {
	// ID is the container id.
	ID string
	// Name is the container name.
	Name string
	// SandboxID is the sandbox id the container belongs to.
	SandboxID string
	// Config is the CRI container config.
	Config *runtime.ContainerConfig
	// ImageRef is the reference of image used by the container.
	ImageRef string
	// Pid is the init process id of the container.
	Pid uint32
	// CreatedAt is the created timestamp.
	CreatedAt int64
	// StartedAt is the started timestamp.
	StartedAt int64
	// FinishedAt is the finished timestamp.
	FinishedAt int64
	// ExitCode is the container exit code.
	ExitCode int32
	// CamelCase string explaining why container is in its current state.
	Reason string
	// Human-readable message indicating details about why container is in its
	// current state.
	Message string
	// Removing indicates that the container is in removing state.
	// In fact, this field doesn't need to be checkpointed.
	// TODO(random-liu): Skip this during serialization when we put object
	// into the store directly.
	// TODO(random-liu): Reset this field to false during state recoverry.
	Removing bool
}

// State returns current state of the container based on the metadata.
func (c *ContainerMetadata) State() runtime.ContainerState {
	if c.FinishedAt != 0 {
		return runtime.ContainerState_CONTAINER_EXITED
	}
	if c.StartedAt != 0 {
		return runtime.ContainerState_CONTAINER_RUNNING
	}
	if c.CreatedAt != 0 {
		return runtime.ContainerState_CONTAINER_CREATED
	}
	return runtime.ContainerState_CONTAINER_UNKNOWN
}

// ContainerUpdateFunc is the function used to update ContainerMetadata.
type ContainerUpdateFunc func(ContainerMetadata) (ContainerMetadata, error)

// ContainerToStoreUpdateFunc generates a metadata store UpdateFunc from ContainerUpdateFunc.
func ContainerToStoreUpdateFunc(u ContainerUpdateFunc) store.UpdateFunc {
	return func(data []byte) ([]byte, error) {
		meta := &ContainerMetadata{}
		if err := json.Unmarshal(data, meta); err != nil {
			return nil, err
		}
		newMeta, err := u(*meta)
		if err != nil {
			return nil, err
		}
		return json.Marshal(newMeta)
	}
}

// ContainerStore is the store for metadata of all containers.
type ContainerStore interface {
	// Create creates a container from ContainerMetadata in the store.
	Create(ContainerMetadata) error
	// Get gets a specified container.
	Get(string) (*ContainerMetadata, error)
	// Update updates a specified container.
	Update(string, ContainerUpdateFunc) error
	// List lists all containers.
	List() ([]*ContainerMetadata, error)
	// Delete deletes the container from the store.
	Delete(string) error
}

// containerStore is an implmentation of ContainerStore.
type containerStore struct {
	store store.MetadataStore
}

// NewContainerStore creates a ContainerStore from a basic MetadataStore.
func NewContainerStore(store store.MetadataStore) ContainerStore {
	return &containerStore{store: store}
}

// Create creates a container from ContainerMetadata in the store.
func (c *containerStore) Create(metadata ContainerMetadata) error {
	data, err := json.Marshal(&metadata)
	if err != nil {
		return err
	}
	return c.store.Create(metadata.ID, data)
}

// Get gets a specified container.
func (c *containerStore) Get(containerID string) (*ContainerMetadata, error) {
	data, err := c.store.Get(containerID)
	if err != nil {
		return nil, err
	}
	container := &ContainerMetadata{}
	if err := json.Unmarshal(data, container); err != nil {
		return nil, err
	}
	return container, nil
}

// Update updates a specified container. The function is running in a
// transaction. Update will not be applied when the update function
// returns error.
func (c *containerStore) Update(containerID string, u ContainerUpdateFunc) error {
	return c.store.Update(containerID, ContainerToStoreUpdateFunc(u))
}

// List lists all containers.
func (c *containerStore) List() ([]*ContainerMetadata, error) {
	allData, err := c.store.List()
	if err != nil {
		return nil, err
	}
	var containers []*ContainerMetadata
	for _, data := range allData {
		container := &ContainerMetadata{}
		if err := json.Unmarshal(data, container); err != nil {
			return nil, err
		}
		containers = append(containers, container)
	}
	return containers, nil
}

// Delete deletes the Container from the store.
func (c *containerStore) Delete(containerID string) error {
	return c.store.Delete(containerID)
}
