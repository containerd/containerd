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

package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	sandboxstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/sandbox"
)

func TestToCRISandbox(t *testing.T) {
	config := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name:      "test-name",
			Uid:       "test-uid",
			Namespace: "test-ns",
			Attempt:   1,
		},
		Labels:      map[string]string{"a": "b"},
		Annotations: map[string]string{"c": "d"},
	}
	createdAt := time.Now().UnixNano()
	meta := sandboxstore.Metadata{
		ID:        "test-id",
		Name:      "test-name",
		Config:    config,
		CreatedAt: createdAt,
		NetNS:     "test-netns",
	}
	state := runtime.PodSandboxState_SANDBOX_READY
	expect := &runtime.PodSandbox{
		Id:          "test-id",
		Metadata:    config.GetMetadata(),
		State:       state,
		CreatedAt:   createdAt,
		Labels:      config.GetLabels(),
		Annotations: config.GetAnnotations(),
	}
	s := toCRISandbox(meta, state)
	assert.Equal(t, expect, s)
}

func TestFilterSandboxes(t *testing.T) {
	c := newTestCRIContainerdService()

	testSandboxes := []*runtime.PodSandbox{
		{
			Id:       "1",
			Metadata: &runtime.PodSandboxMetadata{Name: "name-1", Uid: "uid-1", Namespace: "ns-1", Attempt: 1},
			State:    runtime.PodSandboxState_SANDBOX_READY,
		},
		{
			Id:       "2",
			Metadata: &runtime.PodSandboxMetadata{Name: "name-2", Uid: "uid-2", Namespace: "ns-2", Attempt: 2},
			State:    runtime.PodSandboxState_SANDBOX_NOTREADY,
			Labels:   map[string]string{"a": "b"},
		},
		{
			Id:       "3",
			Metadata: &runtime.PodSandboxMetadata{Name: "name-2", Uid: "uid-2", Namespace: "ns-2", Attempt: 2},
			State:    runtime.PodSandboxState_SANDBOX_READY,
			Labels:   map[string]string{"c": "d"},
		},
	}
	for desc, test := range map[string]struct {
		filter *runtime.PodSandboxFilter
		expect []*runtime.PodSandbox
	}{
		"no filter": {
			expect: testSandboxes,
		},
		"id filter": {
			filter: &runtime.PodSandboxFilter{Id: "2"},
			expect: []*runtime.PodSandbox{testSandboxes[1]},
		},
		"state filter": {
			filter: &runtime.PodSandboxFilter{
				State: &runtime.PodSandboxStateValue{
					State: runtime.PodSandboxState_SANDBOX_READY,
				},
			},
			expect: []*runtime.PodSandbox{testSandboxes[0], testSandboxes[2]},
		},
		"label filter": {
			filter: &runtime.PodSandboxFilter{
				LabelSelector: map[string]string{"a": "b"},
			},
			expect: []*runtime.PodSandbox{testSandboxes[1]},
		},
		"mixed filter not matched": {
			filter: &runtime.PodSandboxFilter{
				Id:            "1",
				LabelSelector: map[string]string{"a": "b"},
			},
			expect: []*runtime.PodSandbox{},
		},
		"mixed filter matched": {
			filter: &runtime.PodSandboxFilter{
				State: &runtime.PodSandboxStateValue{
					State: runtime.PodSandboxState_SANDBOX_READY,
				},
				LabelSelector: map[string]string{"c": "d"},
			},
			expect: []*runtime.PodSandbox{testSandboxes[2]},
		},
	} {
		filtered := c.filterCRISandboxes(testSandboxes, test.filter)
		assert.Equal(t, test.expect, filtered, desc)
	}
}
