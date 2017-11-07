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
	createdAt := time.Now()
	meta := sandboxstore.Metadata{
		ID:        "test-id",
		Name:      "test-name",
		Config:    config,
		NetNSPath: "test-netns",
	}
	state := runtime.PodSandboxState_SANDBOX_READY
	expect := &runtime.PodSandbox{
		Id:          "test-id",
		Metadata:    config.GetMetadata(),
		State:       state,
		CreatedAt:   createdAt.UnixNano(),
		Labels:      config.GetLabels(),
		Annotations: config.GetAnnotations(),
	}
	s := toCRISandbox(meta, state, createdAt)
	assert.Equal(t, expect, s)
}

func TestFilterSandboxes(t *testing.T) {
	c := newTestCRIContainerdService()
	sandboxes := []struct {
		sandbox sandboxstore.Sandbox
		state   runtime.PodSandboxState
	}{
		{
			sandbox: sandboxstore.Sandbox{
				Metadata: sandboxstore.Metadata{
					ID:   "1abcdef",
					Name: "sandboxname-1",
					Config: &runtime.PodSandboxConfig{
						Metadata: &runtime.PodSandboxMetadata{
							Name:      "podname-1",
							Uid:       "uid-1",
							Namespace: "ns-1",
							Attempt:   1,
						},
					},
				},
			},
			state: runtime.PodSandboxState_SANDBOX_READY,
		},
		{
			sandbox: sandboxstore.Sandbox{
				Metadata: sandboxstore.Metadata{
					ID:   "2abcdef",
					Name: "sandboxname-2",
					Config: &runtime.PodSandboxConfig{
						Metadata: &runtime.PodSandboxMetadata{
							Name:      "podname-2",
							Uid:       "uid-2",
							Namespace: "ns-2",
							Attempt:   2,
						},
						Labels: map[string]string{"a": "b"},
					},
				},
			},
			state: runtime.PodSandboxState_SANDBOX_NOTREADY,
		},
		{
			sandbox: sandboxstore.Sandbox{
				Metadata: sandboxstore.Metadata{
					ID:   "3abcdef",
					Name: "sandboxname-3",
					Config: &runtime.PodSandboxConfig{
						Metadata: &runtime.PodSandboxMetadata{
							Name:      "podname-2",
							Uid:       "uid-2",
							Namespace: "ns-2",
							Attempt:   2,
						},
						Labels: map[string]string{"c": "d"},
					},
				},
			},
			state: runtime.PodSandboxState_SANDBOX_READY,
		},
	}

	// Create PodSandbox
	testSandboxes := []*runtime.PodSandbox{}
	createdAt := time.Now()
	for _, sb := range sandboxes {
		testSandboxes = append(testSandboxes, toCRISandbox(sb.sandbox.Metadata, sb.state, createdAt))
	}

	// Inject test sandbox metadata
	for _, sb := range sandboxes {
		assert.NoError(t, c.sandboxStore.Add(sb.sandbox))
	}

	for desc, test := range map[string]struct {
		filter *runtime.PodSandboxFilter
		expect []*runtime.PodSandbox
	}{
		"no filter": {
			expect: testSandboxes,
		},
		"id filter": {
			filter: &runtime.PodSandboxFilter{Id: "2abcdef"},
			expect: []*runtime.PodSandbox{testSandboxes[1]},
		},
		"truncid filter": {
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
		t.Logf("TestCase: %s", desc)
		filtered := c.filterCRISandboxes(testSandboxes, test.filter)
		assert.Equal(t, test.expect, filtered, desc)
	}
}
