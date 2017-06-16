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
	"testing"
	"time"

	assertlib "github.com/stretchr/testify/assert"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata/store"

	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"
)

func TestSandboxStore(t *testing.T) {
	sandboxes := map[string]*SandboxMetadata{
		"1": {
			ID:   "1",
			Name: "Sandbox-1",
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{
					Name:      "TestPod-1",
					Uid:       "TestUid-1",
					Namespace: "TestNamespace-1",
					Attempt:   1,
				},
			},
			CreatedAt: time.Now().UnixNano(),
			NetNS:     "TestNetNS-1",
			Pid:       1001,
		},
		"2": {
			ID:   "2",
			Name: "Sandbox-2",
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{
					Name:      "TestPod-2",
					Uid:       "TestUid-2",
					Namespace: "TestNamespace-2",
					Attempt:   2,
				},
			},
			CreatedAt: time.Now().UnixNano(),
			NetNS:     "TestNetNS-2",
			Pid:       1002,
		},
		"3": {
			ID:   "3",
			Name: "Sandbox-3",
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{
					Name:      "TestPod-3",
					Uid:       "TestUid-3",
					Namespace: "TestNamespace-3",
					Attempt:   3,
				},
			},
			CreatedAt: time.Now().UnixNano(),
			NetNS:     "TestNetNS-3",
			Pid:       1003,
		},
	}
	assert := assertlib.New(t)

	s := NewSandboxStore(store.NewMetadataStore())

	t.Logf("should be able to create sandbox metadata")
	for _, meta := range sandboxes {
		assert.NoError(s.Create(*meta))
	}

	t.Logf("should be able to get sandbox metadata")
	for id, expectMeta := range sandboxes {
		meta, err := s.Get(id)
		assert.NoError(err)
		assert.Equal(expectMeta, meta)
	}

	t.Logf("should be able to list sandbox metadata")
	sbs, err := s.List()
	assert.NoError(err)
	assert.Len(sbs, 3)

	t.Logf("should be able to update sandbox metadata")
	testID := "2"
	newCreatedAt := time.Now().UnixNano()
	expectMeta := *sandboxes[testID]
	expectMeta.CreatedAt = newCreatedAt
	err = s.Update(testID, func(o SandboxMetadata) (SandboxMetadata, error) {
		o.CreatedAt = newCreatedAt
		return o, nil
	})
	assert.NoError(err)
	newMeta, err := s.Get(testID)
	assert.NoError(err)
	assert.Equal(&expectMeta, newMeta)

	t.Logf("should be able to delete sandbox metadata")
	assert.NoError(s.Delete(testID))
	sbs, err = s.List()
	assert.NoError(err)
	assert.Len(sbs, 2)

	t.Logf("get should return nil with not exist error after deletion")
	meta, err := s.Get(testID)
	assert.Error(store.ErrNotExist, err)
	assert.Nil(meta)
}
