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

func TestPodSandboxStatus(t *testing.T) {
	const (
		id = "test-id"
		ip = "10.10.10.10"
	)
	createdAt := time.Now()
	config := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name:      "test-name",
			Uid:       "test-uid",
			Namespace: "test-ns",
			Attempt:   1,
		},
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{
					HostNetwork: true,
					HostPid:     false,
					HostIpc:     true,
				},
			},
		},
		Labels:      map[string]string{"a": "b"},
		Annotations: map[string]string{"c": "d"},
	}
	sandbox := &sandboxstore.Sandbox{
		Metadata: sandboxstore.Metadata{
			ID:     id,
			Name:   "test-name",
			Config: config,
		},
	}
	state := runtime.PodSandboxState_SANDBOX_NOTREADY

	expected := &runtime.PodSandboxStatus{
		Id:        id,
		Metadata:  config.GetMetadata(),
		State:     state,
		CreatedAt: createdAt.UnixNano(),
		Network:   &runtime.PodSandboxNetworkStatus{Ip: ip},
		Linux: &runtime.LinuxPodSandboxStatus{
			Namespaces: &runtime.Namespace{
				Options: &runtime.NamespaceOption{
					HostNetwork: true,
					HostPid:     false,
					HostIpc:     true,
				},
			},
		},
		Labels:      config.GetLabels(),
		Annotations: config.GetAnnotations(),
	}

	got := toCRISandboxStatus(sandbox.Metadata, state, createdAt, ip)
	assert.Equal(t, expected, got)
}
