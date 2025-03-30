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
	"os"
	goruntime "runtime"
	"testing"
	"time"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	runcoptions "github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/containerd/v2/core/containers"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/containerd/v2/pkg/protobuf/types"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/typeurl/v2"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetUserFromImage tests the logic of getting image uid or user name of image user.
func TestGetUserFromImage(t *testing.T) {
	newI64 := func(i int64) *int64 { return &i }
	for _, test := range []struct {
		desc string
		user string
		uid  *int64
		name string
	}{
		{
			desc: "no gid",
			user: "0",
			uid:  newI64(0),
		},
		{
			desc: "uid/gid",
			user: "0:1",
			uid:  newI64(0),
		},
		{
			desc: "empty user",
			user: "",
		},
		{
			desc: "multiple separators",
			user: "1:2:3",
			uid:  newI64(1),
		},
		{
			desc: "root username",
			user: "root:root",
			name: "root",
		},
		{
			desc: "username",
			user: "test:test",
			name: "test",
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			actualUID, actualName := getUserFromImage(test.user)
			assert.Equal(t, test.uid, actualUID)
			assert.Equal(t, test.name, actualName)
		})
	}
}

func TestGenerateRuntimeOptions(t *testing.T) {
	nilOpts := `
systemd_cgroup = true
[containerd]
  no_pivot = true
  default_runtime_name = "default"
[containerd.runtimes.runcv2]
  runtime_type = "` + plugins.RuntimeRuncV2 + `"
`
	nonNilOpts := `
systemd_cgroup = true
[containerd]
  no_pivot = true
  default_runtime_name = "default"
[containerd.runtimes.legacy.options]
  Runtime = "legacy"
  RuntimeRoot = "/legacy"
[containerd.runtimes.runc.options]
  BinaryName = "runc"
  Root = "/runc"
  NoNewKeyring = true
[containerd.runtimes.runcv2]
  runtime_type = "` + plugins.RuntimeRuncV2 + `"
[containerd.runtimes.runcv2.options]
  BinaryName = "runc"
  Root = "/runcv2"
  NoNewKeyring = true
`
	var nilOptsConfig, nonNilOptsConfig criconfig.Config
	err := toml.Unmarshal([]byte(nilOpts), &nilOptsConfig)
	require.NoError(t, err)
	require.Len(t, nilOptsConfig.Runtimes, 1)

	err = toml.Unmarshal([]byte(nonNilOpts), &nonNilOptsConfig)
	require.NoError(t, err)
	require.Len(t, nonNilOptsConfig.Runtimes, 3)

	for _, test := range []struct {
		desc            string
		r               criconfig.Runtime
		c               criconfig.Config
		expectedOptions interface{}
	}{
		{
			desc:            "when options is nil, should return nil option for io.containerd.runc.v2",
			r:               nilOptsConfig.Runtimes["runcv2"],
			c:               nilOptsConfig,
			expectedOptions: nil,
		},
		{
			desc: "when options is not nil, should be able to decode for io.containerd.runc.v2",
			r:    nonNilOptsConfig.Runtimes["runcv2"],
			c:    nonNilOptsConfig,
			expectedOptions: &runcoptions.Options{
				BinaryName:   "runc",
				Root:         "/runcv2",
				NoNewKeyring: true,
			},
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			opts, err := criconfig.GenerateRuntimeOptions(test.r)
			assert.NoError(t, err)
			assert.Equal(t, test.expectedOptions, opts)
		})
	}
}

func TestEnvDeduplication(t *testing.T) {
	for _, test := range []struct {
		desc     string
		existing []string
		kv       [][2]string
		expected []string
	}{
		{
			desc: "single env",
			kv: [][2]string{
				{"a", "b"},
			},
			expected: []string{"a=b"},
		},
		{
			desc: "multiple envs",
			kv: [][2]string{
				{"a", "b"},
				{"c", "d"},
				{"e", "f"},
			},
			expected: []string{
				"a=b",
				"c=d",
				"e=f",
			},
		},
		{
			desc: "env override",
			kv: [][2]string{
				{"k1", "v1"},
				{"k2", "v2"},
				{"k3", "v3"},
				{"k3", "v4"},
				{"k1", "v5"},
				{"k4", "v6"},
			},
			expected: []string{
				"k1=v5",
				"k2=v2",
				"k3=v4",
				"k4=v6",
			},
		},
		{
			desc: "existing env",
			existing: []string{
				"k1=v1",
				"k2=v2",
				"k3=v3",
			},
			kv: [][2]string{
				{"k3", "v4"},
				{"k2", "v5"},
				{"k4", "v6"},
			},
			expected: []string{
				"k1=v1",
				"k2=v5",
				"k3=v4",
				"k4=v6",
			},
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			var spec runtimespec.Spec
			if len(test.existing) > 0 {
				spec.Process = &runtimespec.Process{
					Env: test.existing,
				}
			}
			for _, kv := range test.kv {
				oci.WithEnv([]string{kv[0] + "=" + kv[1]})(context.Background(), nil, nil, &spec)
			}
			assert.Equal(t, test.expected, spec.Process.Env)
		})
	}
}

func TestEnsureRemoveAllNotExist(t *testing.T) {
	// should never return an error for a non-existent path
	if err := ensureRemoveAll(context.Background(), "/non/existent/path"); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureRemoveAllWithDir(t *testing.T) {
	dir := t.TempDir()
	if err := ensureRemoveAll(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureRemoveAllWithFile(t *testing.T) {
	tmp, err := os.CreateTemp("", "test-ensure-removeall-with-dir")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	if err := ensureRemoveAll(context.Background(), tmp.Name()); err != nil {
		t.Fatal(err)
	}
}

// Helper function for setting up an environment to test PID namespace targeting.
func addContainer(c *criService, containerID, sandboxID string, PID uint32, createdAt, startedAt, finishedAt int64) error {
	meta := containerstore.Metadata{
		ID:        containerID,
		SandboxID: sandboxID,
	}
	status := containerstore.Status{
		Pid:        PID,
		CreatedAt:  createdAt,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	}
	container, err := containerstore.NewContainer(meta,
		containerstore.WithFakeStatus(status),
	)
	if err != nil {
		return err
	}
	return c.containerStore.Add(container)
}

func TestValidateTargetContainer(t *testing.T) {
	testSandboxID := "test-sandbox-uid"

	// The existing container that will be targeted.
	testTargetContainerID := "test-target-container"
	testTargetContainerPID := uint32(4567)

	// A container that has finished running and cannot be targeted.
	testStoppedContainerID := "stopped-target-container"
	testStoppedContainerPID := uint32(6789)

	// A container from another pod.
	testOtherContainerSandboxID := "other-sandbox-uid"
	testOtherContainerID := "other-target-container"
	testOtherContainerPID := uint32(7890)

	// Container create/start/stop times.
	createdAt := time.Now().Add(-15 * time.Second).UnixNano()
	startedAt := time.Now().Add(-10 * time.Second).UnixNano()
	finishedAt := time.Now().Add(-5 * time.Second).UnixNano()

	c := newTestCRIService()

	// Create a target container.
	err := addContainer(c, testTargetContainerID, testSandboxID, testTargetContainerPID, createdAt, startedAt, 0)
	require.NoError(t, err, "error creating test target container")

	// Create a stopped container.
	err = addContainer(c, testStoppedContainerID, testSandboxID, testStoppedContainerPID, createdAt, startedAt, finishedAt)
	require.NoError(t, err, "error creating test stopped container")

	// Create a container in another pod.
	err = addContainer(c, testOtherContainerID, testOtherContainerSandboxID, testOtherContainerPID, createdAt, startedAt, 0)
	require.NoError(t, err, "error creating test container in other pod")

	for _, test := range []struct {
		desc              string
		targetContainerID string
		expectError       bool
	}{
		{
			desc:              "target container in pod",
			targetContainerID: testTargetContainerID,
			expectError:       false,
		},
		{
			desc:              "target stopped container in pod",
			targetContainerID: testStoppedContainerID,
			expectError:       true,
		},
		{
			desc:              "target container does not exist",
			targetContainerID: "no-container-with-this-id",
			expectError:       true,
		},
		{
			desc:              "target container in other pod",
			targetContainerID: testOtherContainerID,
			expectError:       true,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			targetContainer, err := c.validateTargetContainer(testSandboxID, test.targetContainerID)
			if test.expectError {
				require.Error(t, err, "target should have been invalid but no error")
				return
			}
			require.NoErrorf(t, err, "target should have been valid but got error")

			assert.Equal(t, test.targetContainerID, targetContainer.ID, "returned target container does not have expected ID")
		})
	}

}

func TestGetRuntimeOptions(t *testing.T) {
	_, err := getRuntimeOptions(containers.Container{})
	require.NoError(t, err)

	var pbany *types.Any               // This is nil.
	var typeurlAny typeurl.Any = pbany // This is typed nil.
	_, err = getRuntimeOptions(containers.Container{Runtime: containers.RuntimeInfo{Options: typeurlAny}})
	require.NoError(t, err)
}

func TestHostNetwork(t *testing.T) {
	tests := []struct {
		name     string
		c        *runtime.PodSandboxConfig
		expected bool
	}{
		{
			name: "when pod namespace return false",
			c: &runtime.PodSandboxConfig{
				Linux: &runtime.LinuxPodSandboxConfig{
					SecurityContext: &runtime.LinuxSandboxSecurityContext{
						NamespaceOptions: &runtime.NamespaceOption{
							Network: runtime.NamespaceMode_POD,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "when node namespace return true",
			c: &runtime.PodSandboxConfig{
				Linux: &runtime.LinuxPodSandboxConfig{
					SecurityContext: &runtime.LinuxSandboxSecurityContext{
						NamespaceOptions: &runtime.NamespaceOption{
							Network: runtime.NamespaceMode_NODE,
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		if goruntime.GOOS != "linux" {
			t.Skip()
		}

		t.Run(tt.name, func(t *testing.T) {
			if hostNetwork(tt.c) != tt.expected {
				t.Errorf("failed hostNetwork got %t expected %t", hostNetwork(tt.c), tt.expected)
			}
		})
	}
}
