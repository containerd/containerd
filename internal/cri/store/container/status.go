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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/containerd/continuity"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// The container state machine in the CRI plugin:
//
//                         +              +
//                         |              |
//                         | Create       | Load
//                         |              |
//                    +----v----+         |
//                    |         |         |
//                    | CREATED <---------+-----------+
//                    |         |         |           |
//                    +----+-----         |           |
//                         |              |           |
//                         | Start        |           |
//                         |              |           |
//                    +----v----+         |           |
//   Exec    +--------+         |         |           |
//  Attach   |        | RUNNING <---------+           |
// LogReopen +-------->         |         |           |
//                    +----+----+         |           |
//                         |              |           |
//                         | Stop/Exit    |           |
//                         |              |           |
//                    +----v----+         |           |
//                    |         <---------+      +----v----+
//                    |  EXITED |                |         |
//                    |         <----------------+ UNKNOWN |
//                    +----+----+       Stop     |         |
//                         |                     +---------+
//                         | Remove
//                         v
//                      DELETED

// statusVersion is current version of container status.
const statusVersion = "v1"

// versionedStatus is the internal used versioned container status.
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
	// Starting indicates that the container is in starting state.
	// This field doesn't need to be checkpointed.
	Starting bool `json:"-"`
	// Removing indicates that the container is in removing state.
	// This field doesn't need to be checkpointed.
	Removing bool `json:"-"`
	// Unknown indicates that the container status is not fully loaded.
	// This field doesn't need to be checkpointed.
	Unknown bool `json:"-"`
	// Resources has container runtime resource constraints
	Resources *runtime.ContainerResources
}

// State returns current state of the container based on the container status.
func (s Status) State() runtime.ContainerState {
	if s.Unknown {
		return runtime.ContainerState_CONTAINER_UNKNOWN
	}
	if s.FinishedAt != 0 {
		return runtime.ContainerState_CONTAINER_EXITED
	}
	if s.StartedAt != 0 {
		return runtime.ContainerState_CONTAINER_RUNNING
	}
	if s.CreatedAt != 0 {
		return runtime.ContainerState_CONTAINER_CREATED
	}
	return runtime.ContainerState_CONTAINER_UNKNOWN
}

// encode encodes Status into bytes in json format.
func (s *Status) encode() ([]byte, error) {
	return json.Marshal(&versionedStatus{
		Version: statusVersion,
		Status:  *s,
	})
}

// decode decodes Status from bytes.
func (s *Status) decode(data []byte) error {
	versioned := &versionedStatus{}
	if err := json.Unmarshal(data, versioned); err != nil {
		return err
	}
	// Handle old version after upgrade.
	switch versioned.Version {
	case statusVersion:
		*s = versioned.Status
		return nil
	}
	return errors.New("unsupported version")
}

// UpdateFunc is function used to update the container status. If there
// is an error, the update will be rolled back.
type UpdateFunc func(Status) (Status, error)

// StatusStorage manages the container status with a storage backend.
type StatusStorage interface {
	// Get a container status.
	Get() Status
	// UpdateSync updates the container status and the on disk checkpoint.
	// Note that the update MUST be applied in one transaction.
	UpdateSync(UpdateFunc) error
	// Update the container status. Note that the update MUST be applied
	// in one transaction.
	Update(UpdateFunc) error
	// Delete the container status.
	// Note:
	// * Delete should be idempotent.
	// * The status must be deleted in one transaction.
	Delete() error
}

// StoreStatus creates the storage containing the passed in container status with the
// specified id.
// The status MUST be created in one transaction.
func StoreStatus(root, id string, status Status) (StatusStorage, error) {
	data, err := status.encode()
	if err != nil {
		return nil, fmt.Errorf("failed to encode status: %w", err)
	}
	path := filepath.Join(root, "status")
	if err := continuity.AtomicWriteFile(path, data, 0600); err != nil {
		return nil, fmt.Errorf("failed to checkpoint status to %q: %w", path, err)
	}
	return &statusStorage{
		path:   path,
		status: status,
	}, nil
}

// LoadStatus loads container status from checkpoint. There shouldn't be threads
// writing to the file during loading.
func LoadStatus(root, id string) (Status, error) {
	path := filepath.Join(root, "status")
	data, err := os.ReadFile(path)
	if err != nil {
		return Status{}, fmt.Errorf("failed to read status from %q: %w", path, err)
	}
	var status Status
	if err := status.decode(data); err != nil {
		return Status{}, fmt.Errorf("failed to decode status %q: %w", data, err)
	}
	return status, nil
}

type statusStorage struct {
	sync.RWMutex
	path   string
	status Status
}

// Get a copy of container status.
func (s *statusStorage) Get() Status {
	s.RLock()
	defer s.RUnlock()
	// Deep copy is needed in case some fields in Status are updated after Get()
	// is called.
	return deepCopyOf(s.status)
}

func deepCopyOf(s Status) Status {
	copy := s
	// Resources is the only field that is a pointer, and therefore needs
	// a manual deep copy.
	// This will need updates when new fields are added to ContainerResources.
	if s.Resources == nil {
		return copy
	}
	copy.Resources = &runtime.ContainerResources{}
	if s.Resources != nil && s.Resources.Linux != nil {
		hugepageLimits := make([]*runtime.HugepageLimit, 0, len(s.Resources.Linux.HugepageLimits))
		for _, l := range s.Resources.Linux.HugepageLimits {
			if l != nil {
				hugepageLimits = append(hugepageLimits, &runtime.HugepageLimit{
					PageSize: l.PageSize,
					Limit:    l.Limit,
				})
			}
		}
		copy.Resources = &runtime.ContainerResources{
			Linux: &runtime.LinuxContainerResources{
				CpuPeriod:              s.Resources.Linux.CpuPeriod,
				CpuQuota:               s.Resources.Linux.CpuQuota,
				CpuShares:              s.Resources.Linux.CpuShares,
				CpusetCpus:             s.Resources.Linux.CpusetCpus,
				CpusetMems:             s.Resources.Linux.CpusetMems,
				MemoryLimitInBytes:     s.Resources.Linux.MemoryLimitInBytes,
				MemorySwapLimitInBytes: s.Resources.Linux.MemorySwapLimitInBytes,
				OomScoreAdj:            s.Resources.Linux.OomScoreAdj,
				Unified:                s.Resources.Linux.Unified,
				HugepageLimits:         hugepageLimits,
			},
		}
	}

	if s.Resources != nil && s.Resources.Windows != nil {
		copy.Resources = &runtime.ContainerResources{
			Windows: &runtime.WindowsContainerResources{
				CpuShares:          s.Resources.Windows.CpuShares,
				CpuCount:           s.Resources.Windows.CpuCount,
				CpuMaximum:         s.Resources.Windows.CpuMaximum,
				MemoryLimitInBytes: s.Resources.Windows.MemoryLimitInBytes,
				RootfsSizeInBytes:  s.Resources.Windows.RootfsSizeInBytes,
			},
		}
	}
	return copy
}

// UpdateSync updates the container status and the on disk checkpoint.
func (s *statusStorage) UpdateSync(u UpdateFunc) error {
	s.Lock()
	defer s.Unlock()
	newStatus, err := u(s.status)
	if err != nil {
		return err
	}
	data, err := newStatus.encode()
	if err != nil {
		return fmt.Errorf("failed to encode status: %w", err)
	}
	if err := continuity.AtomicWriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("failed to checkpoint status to %q: %w", s.path, err)
	}
	s.status = newStatus
	return nil
}

// Update the container status.
func (s *statusStorage) Update(u UpdateFunc) error {
	s.Lock()
	defer s.Unlock()
	newStatus, err := u(s.status)
	if err != nil {
		return err
	}
	s.status = newStatus
	return nil
}

// Delete deletes the container status from disk atomically.
func (s *statusStorage) Delete() error {
	temp := filepath.Dir(s.path) + ".del-" + filepath.Base(s.path)
	if err := os.Rename(s.path, temp); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.RemoveAll(temp)
}
