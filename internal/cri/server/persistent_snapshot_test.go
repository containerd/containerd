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
	"testing"

	"github.com/containerd/containerd/v2/internal/cri/annotations"
	assertlib "github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestPersistentSnapshotFromPodAnnotations(t *testing.T) {
	assert := assertlib.New(t)

	meta, err := persistentSnapshotFromPodAnnotations(&runtime.PodSandboxConfig{
		Annotations: map[string]string{
			annotations.PersistEnabled: "true",
			annotations.PersistID:      "user-a-devbox",
		},
	}, "box", "sha256:image")
	assert.NoError(err)
	assert.Equal("user-a-devbox", meta.ID)
	assert.Equal("persist/user-a-devbox-box", meta.SnapshotKey)
	assert.Equal("sha256:image", meta.ImageRef)

	meta, err = persistentSnapshotFromPodAnnotations(&runtime.PodSandboxConfig{
		Annotations: map[string]string{
			annotations.PersistEnabled:     "true",
			annotations.PersistID:          "user-a-devbox",
			annotations.PersistImageDigest: "sha256:image",
		},
	}, "box", "sha256:image")
	assert.NoError(err)
	assert.Equal("sha256:image", meta.ImageRef)
}

func TestPersistentSnapshotFromPodAnnotationsDisabled(t *testing.T) {
	assert := assertlib.New(t)
	meta, err := persistentSnapshotFromPodAnnotations(&runtime.PodSandboxConfig{}, "box", "sha256:image")
	assert.NoError(err)
	assert.Nil(meta)
}

func TestPersistentSnapshotFromPodAnnotationsRejectsInvalidID(t *testing.T) {
	assert := assertlib.New(t)
	_, err := persistentSnapshotFromPodAnnotations(&runtime.PodSandboxConfig{
		Annotations: map[string]string{
			annotations.PersistEnabled: "true",
			annotations.PersistID:      "../bad",
		},
	}, "box", "sha256:image")
	assert.Error(err)
}

func TestPersistentSnapshotFromPodAnnotationsRejectsImageMismatch(t *testing.T) {
	assert := assertlib.New(t)
	_, err := persistentSnapshotFromPodAnnotations(&runtime.PodSandboxConfig{
		Annotations: map[string]string{
			annotations.PersistEnabled:     "true",
			annotations.PersistID:          "user-a-devbox",
			annotations.PersistImageDigest: "sha256:other",
		},
	}, "box", "sha256:image")
	assert.Error(err)
}
