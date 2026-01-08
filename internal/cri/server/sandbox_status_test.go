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
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/types"
)

func TestPodSandboxStatus(t *testing.T) {
	const (
		id = "test-id"
		ip = "10.10.10.10"
	)
	idmap := []*runtime.IDMapping{
		{
			ContainerId: 0,
			HostId:      100,
			Length:      1,
		},
	}
	additionalIPs := []string{"8.8.8.8", "2001:db8:85a3::8a2e:370:7334"}
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
					Network: runtime.NamespaceMode_NODE,
					Pid:     runtime.NamespaceMode_CONTAINER,
					Ipc:     runtime.NamespaceMode_POD,
					UsernsOptions: &runtime.UserNamespace{
						Uids: idmap,
						Gids: idmap,
						Mode: runtime.NamespaceMode_POD,
					},
				},
			},
		},
		Labels:      map[string]string{"a": "b"},
		Annotations: map[string]string{"c": "d"},
	}
	metadata := sandboxstore.Metadata{
		ID:             id,
		Name:           "test-name",
		Config:         config,
		RuntimeHandler: "test-runtime-handler",
	}

	expected := &runtime.PodSandboxStatus{
		Id:        id,
		Metadata:  config.GetMetadata(),
		CreatedAt: createdAt.UnixNano(),
		Network: &runtime.PodSandboxNetworkStatus{
			Ip: ip,
			AdditionalIps: []*runtime.PodIP{
				{
					Ip: additionalIPs[0],
				},
				{
					Ip: additionalIPs[1],
				},
			},
		},
		Linux: &runtime.LinuxPodSandboxStatus{
			Namespaces: &runtime.Namespace{
				Options: &runtime.NamespaceOption{
					Network: runtime.NamespaceMode_NODE,
					Pid:     runtime.NamespaceMode_CONTAINER,
					Ipc:     runtime.NamespaceMode_POD,
					UsernsOptions: &runtime.UserNamespace{
						Uids: idmap,
						Gids: idmap,
						Mode: runtime.NamespaceMode_POD,
					},
				},
			},
		},
		Labels:         config.GetLabels(),
		Annotations:    config.GetAnnotations(),
		RuntimeHandler: "test-runtime-handler",
	}
	for _, test := range []struct {
		desc          string
		state         string
		expectedState runtime.PodSandboxState
	}{
		{
			desc:          "sandbox state ready",
			state:         sandboxstore.StateReady.String(),
			expectedState: runtime.PodSandboxState_SANDBOX_READY,
		},
		{
			desc:          "sandbox state not ready",
			state:         sandboxstore.StateNotReady.String(),
			expectedState: runtime.PodSandboxState_SANDBOX_NOTREADY,
		},
		{
			desc:          "sandbox state unknown",
			state:         sandboxstore.StateUnknown.String(),
			expectedState: runtime.PodSandboxState_SANDBOX_NOTREADY,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			expected.State = test.expectedState
			got := toCRISandboxStatus(metadata, test.state, createdAt, ip, additionalIPs)
			assert.Equal(t, expected, got)
		})
	}
}

func TestSetUpdatedResources(t *testing.T) {
	overhead := &runtime.LinuxContainerResources{
		CpuPeriod: 100,
	}
	resources := &runtime.LinuxContainerResources{
		CpuPeriod: 200,
	}

	tests := []struct {
		desc            string
		status          sandboxstore.Status
		info            map[string]string
		expectOverhead  *runtime.LinuxContainerResources
		expectResources *runtime.LinuxContainerResources
		expectErr       bool
	}{
		{
			desc: "should update info with resources",
			status: sandboxstore.Status{
				Overhead:  &runtime.ContainerResources{Linux: overhead},
				Resources: &runtime.ContainerResources{Linux: resources},
			},
			info: map[string]string{
				"info": `{"pid": 1234}`,
			},
			expectOverhead:  overhead,
			expectResources: resources,
		},
		{
			desc:   "should not update if resources are nil",
			status: sandboxstore.Status{},
			info: map[string]string{
				"info": `{"pid": 1234}`,
			},
			expectOverhead:  nil,
			expectResources: nil,
		},
		{
			desc:   "should return error on invalid json",
			status: sandboxstore.Status{},
			info: map[string]string{
				"info": `invalid-json`,
			},
			expectErr: true,
		},
		{
			desc:            "should handle nil info map gracefully",
			status:          sandboxstore.Status{},
			info:            nil,
			expectOverhead:  nil,
			expectResources: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			sb := sandboxstore.NewSandbox(sandboxstore.Metadata{}, test.status)
			err := setUpdatedResources(context.Background(), sb, test.info)
			if test.expectErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)

			if test.info == nil {
				return
			}

			var info types.SandboxInfo
			err = json.Unmarshal([]byte(test.info["info"]), &info)
			assert.NoError(t, err)

			if test.expectOverhead != nil {
				assert.Equal(t, test.expectOverhead, info.Overhead.Linux)
			} else {
				assert.Nil(t, info.Overhead)
			}

			if test.expectResources != nil {
				assert.Equal(t, test.expectResources, info.Resources.Linux)
			} else {
				assert.Nil(t, info.Resources)
			}

			// Verify original fields are preserved
			if test.desc == "should update info with resources" {
				assert.Equal(t, uint32(1234), info.Pid)
			}
		})
	}
}
