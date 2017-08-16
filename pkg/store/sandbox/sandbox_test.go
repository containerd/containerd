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
	"time"

	assertlib "github.com/stretchr/testify/assert"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	"github.com/kubernetes-incubator/cri-containerd/pkg/store"
)

func TestSandboxStore(t *testing.T) {
	ids := []string{"1", "2", "3"}
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
			CreatedAt: time.Now().UnixNano(),
			Pid:       1001,
			NetNS:     "TestNetNS-1",
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
			Pid:       1002,
			NetNS:     "TestNetNS-2",
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
			Pid:       1003,
			NetNS:     "TestNetNS-3",
		},
	}
	assert := assertlib.New(t)
	sandboxes := map[string]Sandbox{}
	for _, id := range ids {
		sandboxes[id] = Sandbox{Metadata: metadatas[id]}
	}

	s := NewStore()

	t.Logf("should be able to add sandbox")
	for _, sb := range sandboxes {
		assert.NoError(s.Add(sb))
	}

	t.Logf("should be able to get sandbox")
	for id, sb := range sandboxes {
		got, err := s.Get(id)
		assert.NoError(err)
		assert.Equal(sb, got)
	}

	t.Logf("should be able to list sandboxes")
	sbs := s.List()
	assert.Len(sbs, 3)

	testID := "2"
	t.Logf("add should return already exists error for duplicated sandbox")
	assert.Equal(store.ErrAlreadyExist, s.Add(sandboxes[testID]))

	t.Logf("should be able to delete sandbox")
	s.Delete(testID)
	sbs = s.List()
	assert.Len(sbs, 2)

	t.Logf("get should return not exist error after deletion")
	sb, err := s.Get(testID)
	assert.Equal(Sandbox{}, sb)
	assert.Equal(store.ErrNotExist, err)
}
