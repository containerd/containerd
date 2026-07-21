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

package annotations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/core/snapshots"
)

func TestSnapshotLabelPrefixMatchesSnapshotsPackage(t *testing.T) {
	assert.Equal(t, snapshots.LabelSnapshotPrefix, SnapshotLabelPrefix)
}

func TestDefaultCRISnapshotLabelsForContainer(t *testing.T) {
	config := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name:      "pod-name",
			Uid:       "pod-uid",
			Namespace: "pod-namespace",
		},
	}

	labels := DefaultCRISnapshotLabelsForContainer("sandbox-id", "container-name", "registry.k8s.io/pause:3.10", config)

	assert.Equal(t, ContainerTypeContainer, labels[SnapshotLabel(ContainerType)])
	assert.Equal(t, "sandbox-id", labels[SnapshotLabel(SandboxID)])
	assert.Equal(t, "pod-namespace", labels[SnapshotLabel(SandboxNamespace)])
	assert.Equal(t, "pod-uid", labels[SnapshotLabel(SandboxUID)])
	assert.Equal(t, "pod-name", labels[SnapshotLabel(SandboxName)])
	assert.Equal(t, "container-name", labels[SnapshotLabel(ContainerName)])
	assert.Equal(t, "registry.k8s.io/pause:3.10", labels[SnapshotLabel(ImageName)])
}

func TestDefaultCRISnapshotLabelsForSandbox(t *testing.T) {
	config := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name:      "pod-name",
			Uid:       "pod-uid",
			Namespace: "pod-namespace",
		},
	}

	labels := DefaultCRISnapshotLabelsForSandbox("sandbox-id", "registry.k8s.io/pause:3.10", config)

	assert.Equal(t, ContainerTypeSandbox, labels[SnapshotLabel(ContainerType)])
	assert.Equal(t, "sandbox-id", labels[SnapshotLabel(SandboxID)])
	assert.Equal(t, "pod-namespace", labels[SnapshotLabel(SandboxNamespace)])
	assert.Equal(t, "pod-uid", labels[SnapshotLabel(SandboxUID)])
	assert.Equal(t, "pod-name", labels[SnapshotLabel(SandboxName)])
	assert.Equal(t, "registry.k8s.io/pause:3.10", labels[SnapshotLabel(SandboxImageName)])
	_, ok := labels[SnapshotLabel(ContainerName)]
	assert.False(t, ok)
	_, ok = labels[SnapshotLabel(ImageName)]
	assert.False(t, ok)
}

func TestMergeSnapshotLabelsAllowsOverrides(t *testing.T) {
	defaults := map[string]string{
		SnapshotLabel(SandboxID):   "default-sandbox-id",
		SnapshotLabel(SandboxName): "default-pod-name",
	}
	overrides := map[string]string{
		SnapshotLabel(SandboxID):        "inherited-sandbox-id",
		"containerd.io/snapshot/custom": "custom-value",
	}

	labels := MergeSnapshotLabels(defaults, overrides)

	assert.Equal(t, "inherited-sandbox-id", labels[SnapshotLabel(SandboxID)])
	assert.Equal(t, "default-pod-name", labels[SnapshotLabel(SandboxName)])
	assert.Equal(t, "custom-value", labels["containerd.io/snapshot/custom"])
	assert.Equal(t, "default-sandbox-id", defaults[SnapshotLabel(SandboxID)])
}
