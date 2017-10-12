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
	"testing"

	assertlib "github.com/stretchr/testify/assert"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	"github.com/kubernetes-incubator/cri-containerd/pkg/store"
)

func TestSandboxStore(t *testing.T) {
	metadatas := map[string]Metadata{
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
			NetNSPath: "TestNetNS-1",
		},
		"2abcd": {
			ID:   "2abcd",
			Name: "Sandbox-2abcd",
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{
					Name:      "TestPod-2abcd",
					Uid:       "TestUid-2abcd",
					Namespace: "TestNamespace-2abcd",
					Attempt:   2,
				},
			},
			NetNSPath: "TestNetNS-2",
		},
		"4a333": {
			ID:   "4a333",
			Name: "Sandbox-4a333",
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{
					Name:      "TestPod-4a333",
					Uid:       "TestUid-4a333",
					Namespace: "TestNamespace-4a333",
					Attempt:   3,
				},
			},
			NetNSPath: "TestNetNS-3",
		},
		"4abcd": {
			ID:   "4abcd",
			Name: "Sandbox-4abcd",
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{
					Name:      "TestPod-4abcd",
					Uid:       "TestUid-4abcd",
					Namespace: "TestNamespace-4abcd",
					Attempt:   1,
				},
			},
			NetNSPath: "TestNetNS-4abcd",
		},
	}
	assert := assertlib.New(t)
	sandboxes := map[string]Sandbox{}
	for id := range metadatas {
		sandboxes[id] = Sandbox{Metadata: metadatas[id]}
	}

	s := NewStore()

	t.Logf("should be able to add sandbox")
	for _, sb := range sandboxes {
		assert.NoError(s.Add(sb))
	}

	t.Logf("should be able to get sandbox")
	genTruncIndex := func(normalName string) string { return normalName[:(len(normalName)+1)/2] }
	for id, sb := range sandboxes {
		got, err := s.Get(genTruncIndex(id))
		assert.NoError(err)
		assert.Equal(sb, got)
	}

	t.Logf("should be able to list sandboxes")
	sbs := s.List()
	assert.Len(sbs, len(sandboxes))

	sbNum := len(sandboxes)
	for testID, v := range sandboxes {
		truncID := genTruncIndex(testID)

		t.Logf("add should return already exists error for duplicated sandbox")
		assert.Equal(store.ErrAlreadyExist, s.Add(v))

		t.Logf("should be able to delete sandbox")
		s.Delete(truncID)
		sbNum--
		sbs = s.List()
		assert.Len(sbs, sbNum)

		t.Logf("get should return not exist error after deletion")
		sb, err := s.Get(truncID)
		assert.Equal(Sandbox{}, sb)
		assert.Equal(store.ErrNotExist, err)
	}
}
