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

package container

import (
	"sync"

	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
)

// TODO(random-liu): Handle versioning.
// TODO(random-liu): Add checkpoint support.

// version is current version of container status.
const version = "v1" // nolint

// versionedStatus is the internal used versioned container status.
// nolint
type versionedStatus struct {
	// Version indicates the version of the versioned container status.
	Version string
	Status
}

// Status is the status of a container.
type Status struct {
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
	// This field doesn't need to be checkpointed.
	// TODO(random-liu): Reset this field to false during state recoverry.
	Removing bool
}

// State returns current state of the container based on the container status.
func (c Status) State() runtime.ContainerState {
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

// UpdateFunc is function used to update the container status. If there
// is an error, the update will be rolled back.
type UpdateFunc func(Status) (Status, error)

// StatusStorage manages the container status with a storage backend.
type StatusStorage interface {
	// Get a container status.
	Get() Status
	// Update the container status. Note that the update MUST be applied
	// in one transaction.
	// TODO(random-liu): Distinguish `UpdateSync` and `Update`, only
	// `UpdateSync` should sync data onto disk, so that disk operation
	// for non-critical status change could be avoided.
	Update(UpdateFunc) error
	// Delete the container status.
	// Note:
	// * Delete should be idempotent.
	// * The status must be deleted in one trasaction.
	Delete() error
}

// TODO(random-liu): Add factory function and configure checkpoint path.

// StoreStatus creates the storage containing the passed in container status with the
// specified id.
// The status MUST be created in one transaction.
func StoreStatus(id string, status Status) (StatusStorage, error) {
	return &statusStorage{status: status}, nil
	// TODO(random-liu): Create the data on disk atomically.
}

// LoadStatus loads container status from checkpoint.
func LoadStatus(id string) (StatusStorage, error) {
	// TODO(random-liu): Load container status from disk.
	return nil, nil
}

type statusStorage struct {
	sync.RWMutex
	status Status
}

// Get a copy of container status.
func (m *statusStorage) Get() Status {
	m.RLock()
	defer m.RUnlock()
	return m.status
}

// Update the container status.
func (m *statusStorage) Update(u UpdateFunc) error {
	m.Lock()
	defer m.Unlock()
	newStatus, err := u(m.status)
	if err != nil {
		return err
	}
	// TODO(random-liu) *Update* existing status on disk atomically,
	// return error if checkpoint failed.
	m.status = newStatus
	return nil
}

// Delete deletes the container status from disk atomically.
func (m *statusStorage) Delete() error {
	// TODO(random-liu): Rename the data on the disk, returns error
	// if fails. No lock is needed because file rename is atomic.
	// TODO(random-liu): Cleanup temporary files generated, do not
	// return error.
	return nil
}
