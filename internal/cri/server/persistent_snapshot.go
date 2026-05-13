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

package server

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/containerd/containerd/v2/internal/cri/annotations"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	"github.com/containerd/errdefs"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const persistentSnapshotKeyPrefix = "persist/"

func persistentSnapshotFromPodAnnotations(config *runtime.PodSandboxConfig, containerName, imageRef string) (*containerstore.PersistentSnapshotMetadata, error) {
	if config == nil {
		return nil, nil
	}
	ann := config.GetAnnotations()
	if !strings.EqualFold(ann[annotations.PersistEnabled], "true") {
		return nil, nil
	}
	id := ann[annotations.PersistID]
	if id == "" {
		return nil, fmt.Errorf("persistent snapshot enabled but %q annotation is empty", annotations.PersistID)
	}
	if err := validatePersistentSnapshotID(id); err != nil {
		return nil, err
	}
	if ref := ann[annotations.PersistImageDigest]; ref != "" && ref != imageRef {
		return nil, fmt.Errorf("persistent snapshot image digest %q does not match resolved image %q", ref, imageRef)
	}
	keySuffix := id
	if containerName != "" {
		if err := validatePersistentSnapshotID(containerName); err != nil {
			return nil, fmt.Errorf("container name cannot be used in persistent snapshot key: %w", err)
		}
		keySuffix = id + "-" + containerName
	}
	return &containerstore.PersistentSnapshotMetadata{
		ID:          id,
		SnapshotKey: persistentSnapshotKeyPrefix + keySuffix,
		ImageRef:    imageRef,
	}, nil
}

func persistentSnapshotLabels(meta *containerstore.PersistentSnapshotMetadata) map[string]string {
	if meta == nil {
		return nil
	}
	return map[string]string{
		annotations.PersistEnabled:     "true",
		annotations.PersistID:          meta.ID,
		annotations.PersistImageDigest: meta.ImageRef,
		"containerd.io/gc.root":        "true",
	}
}

func validatePersistentSnapshotID(id string) error {
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '.', '_', '-':
			continue
		default:
			return fmt.Errorf("persistent snapshot id %q contains invalid character %q", id, r)
		}
	}
	return nil
}

func (c *criService) validatePersistentSnapshotAvailable(containerID, snapshotKey string) error {
	for _, cntr := range c.containerStore.List() {
		if cntr.ID == containerID || cntr.PersistentSnapshot == nil {
			continue
		}
		if cntr.PersistentSnapshot.SnapshotKey != snapshotKey {
			continue
		}
		if cntr.Status.Get().State() == runtime.ContainerState_CONTAINER_EXITED {
			continue
		}
		return fmt.Errorf("persistent snapshot %q is already used by container %q: %w", snapshotKey, cntr.ID, errdefs.ErrAlreadyExists)
	}
	return nil
}
