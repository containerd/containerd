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

package opts

import (
	"context"
	"sort"
	"testing"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/oci"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestOrderedMounts(t *testing.T) {
	mounts := []*runtime.Mount{
		{ContainerPath: "/a/b/c"},
		{ContainerPath: "/a/b"},
		{ContainerPath: "/a/b/c/d"},
		{ContainerPath: "/a"},
		{ContainerPath: "/b"},
		{ContainerPath: "/b/c"},
	}
	expected := []*runtime.Mount{
		{ContainerPath: "/a"},
		{ContainerPath: "/b"},
		{ContainerPath: "/a/b"},
		{ContainerPath: "/b/c"},
		{ContainerPath: "/a/b/c"},
		{ContainerPath: "/a/b/c/d"},
	}
	sort.Stable(orderedMounts(mounts))
	assert.Equal(t, expected, mounts)
}

func TestWithPodNamespacesPidZeroFails(t *testing.T) {
	// WithPodNamespaces with pid==0 must fail closed, not produce /proc/0/ns/*
	config := &runtime.LinuxContainerSecurityContext{
		NamespaceOptions: &runtime.NamespaceOption{Pid: runtime.NamespaceMode_POD},
	}
	spec := &runtimespec.Spec{Linux: &runtimespec.Linux{}}
	err := WithPodNamespaces(config, 0, 0, nil, nil)(context.Background(), nil, &containers.Container{}, spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pid is 0")
	for _, ns := range spec.Linux.Namespaces {
		assert.NotEqual(t, "/proc/0/ns/net", ns.Path)
		assert.NotEqual(t, "/proc/0/ns/ipc", ns.Path)
		assert.NotEqual(t, "/proc/0/ns/uts", ns.Path)
	}
}

func TestWithPodNamespacesWithPaths(t *testing.T) {
	podConfig := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{
					Network: runtime.NamespaceMode_POD,
					Pid:     runtime.NamespaceMode_POD,
					Ipc:     runtime.NamespaceMode_POD,
				},
			},
		},
	}
	containerConfig := &runtime.LinuxContainerSecurityContext{
		NamespaceOptions: &runtime.NamespaceOption{Pid: runtime.NamespaceMode_POD},
	}
	explicit := PodNamespacePaths{
		Net: "/run/pinned/net",
		IPC: "/run/pinned/ipc",
		UTS: "/run/pinned/uts",
		PID: "/run/pinned/pid",
	}
	spec := &runtimespec.Spec{Linux: &runtimespec.Linux{}}
	err := WithPodNamespacesWithPaths(containerConfig, podConfig, 0, 0, explicit, nil, nil)(context.Background(), nil, &containers.Container{}, spec)
	require.NoError(t, err)
	assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{Type: runtimespec.NetworkNamespace, Path: "/run/pinned/net"})
	assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{Type: runtimespec.IPCNamespace, Path: "/run/pinned/ipc"})
	assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{Type: runtimespec.UTSNamespace, Path: "/run/pinned/uts"})
	assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{Type: runtimespec.PIDNamespace, Path: "/run/pinned/pid"})
}

func TestWithPodNamespacesWithPathsMissingFailsClosed(t *testing.T) {
	podConfig := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{
					Network: runtime.NamespaceMode_POD,
					Pid:     runtime.NamespaceMode_POD,
					Ipc:     runtime.NamespaceMode_POD,
				},
			},
		},
	}
	containerConfig := &runtime.LinuxContainerSecurityContext{
		NamespaceOptions: &runtime.NamespaceOption{Pid: runtime.NamespaceMode_POD},
	}
	// Explicit missing, pid==0 -> fail closed
	spec := &runtimespec.Spec{Linux: &runtimespec.Linux{}}
	err := WithPodNamespacesWithPaths(containerConfig, podConfig, 0, 0, PodNamespacePaths{}, nil, nil)(context.Background(), nil, &containers.Container{}, spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "explicit namespace path required")
}

func TestWithPodNamespacesWithPathsPidZeroDoesNotUseProc0(t *testing.T) {
	podConfig := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{
					Network: runtime.NamespaceMode_POD,
					Pid:     runtime.NamespaceMode_POD,
					Ipc:     runtime.NamespaceMode_POD,
				},
			},
		},
	}
	containerConfig := &runtime.LinuxContainerSecurityContext{
		NamespaceOptions: &runtime.NamespaceOption{Pid: runtime.NamespaceMode_POD},
	}
	spec := &runtimespec.Spec{Linux: &runtimespec.Linux{}}
	err := WithPodNamespacesWithPaths(containerConfig, podConfig, 0, 0, PodNamespacePaths{}, nil, nil)(context.Background(), nil, &containers.Container{}, spec)
	require.Error(t, err)
	// Ensure no /proc/0 path was ever used
	for _, ns := range spec.Linux.Namespaces {
		assert.NotContains(t, ns.Path, "/proc/0/")
	}
}

func TestWithPodNamespacesWithPathsMultipleNamespaces(t *testing.T) {
	podConfig := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{
					Network: runtime.NamespaceMode_POD,
					Pid:     runtime.NamespaceMode_POD,
					Ipc:     runtime.NamespaceMode_POD,
				},
			},
		},
	}
	containerConfig := &runtime.LinuxContainerSecurityContext{
		NamespaceOptions: &runtime.NamespaceOption{Pid: runtime.NamespaceMode_POD},
	}
	tests := []struct {
		name        string
		pid         uint32
		paths       PodNamespacePaths
		expectPaths map[runtimespec.LinuxNamespaceType]string
		expectError bool
	}{
		{
			name:  "pid-derived fallback",
			pid:   1234,
			paths: PodNamespacePaths{},
			expectPaths: map[runtimespec.LinuxNamespaceType]string{
				runtimespec.NetworkNamespace: "/proc/1234/ns/net",
				runtimespec.IPCNamespace:     "/proc/1234/ns/ipc",
				runtimespec.UTSNamespace:     "/proc/1234/ns/uts",
				runtimespec.PIDNamespace:     "/proc/1234/ns/pid",
			},
		},
		{
			name: "explicit overrides pid",
			pid:  1234,
			paths: PodNamespacePaths{
				Net: "/pinned/net",
				IPC: "/pinned/ipc",
				UTS: "/pinned/uts",
				PID: "/pinned/pid",
			},
			expectPaths: map[runtimespec.LinuxNamespaceType]string{
				runtimespec.NetworkNamespace: "/pinned/net",
				runtimespec.IPCNamespace:     "/pinned/ipc",
				runtimespec.UTSNamespace:     "/pinned/uts",
				runtimespec.PIDNamespace:     "/pinned/pid",
			},
		},
		{
			name:        "pid zero without explicit fails",
			pid:         0,
			paths:       PodNamespacePaths{},
			expectError: true,
		},
		{
			name: "pid zero with explicit succeeds",
			pid:  0,
			paths: PodNamespacePaths{
				Net: "/pinned/net",
				IPC: "/pinned/ipc",
				UTS: "/pinned/uts",
				PID: "/pinned/pid",
			},
			expectPaths: map[runtimespec.LinuxNamespaceType]string{
				runtimespec.NetworkNamespace: "/pinned/net",
				runtimespec.IPCNamespace:     "/pinned/ipc",
				runtimespec.UTSNamespace:     "/pinned/uts",
				runtimespec.PIDNamespace:     "/pinned/pid",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := &runtimespec.Spec{Linux: &runtimespec.Linux{}}
			err := WithPodNamespacesWithPaths(containerConfig, podConfig, tc.pid, tc.pid, tc.paths, nil, nil)(context.Background(), nil, &containers.Container{}, spec)
			if tc.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			for typ, wantPath := range tc.expectPaths {
				found := false
				for _, ns := range spec.Linux.Namespaces {
					if ns.Type == typ && ns.Path == wantPath {
						found = true
						break
					}
				}
				assert.True(t, found, "expected namespace %s with path %s", typ, wantPath)
			}
			// Verify oci.WithLinuxNamespace was applied via oci helper
			require.NoError(t, oci.WithLinuxNamespace(runtimespec.LinuxNamespace{Type: runtimespec.NetworkNamespace, Path: "x"})(context.Background(), nil, nil, spec))
		})
	}
}

func TestWithPodNamespacesBackwardCompat(t *testing.T) {
	// Existing pause-container behavior must continue to work via pid-derived paths.
	config := &runtime.LinuxContainerSecurityContext{
		NamespaceOptions: &runtime.NamespaceOption{Pid: runtime.NamespaceMode_POD},
	}
	spec := &runtimespec.Spec{Linux: &runtimespec.Linux{}}
	err := WithPodNamespaces(config, 1234, 1234, nil, nil)(context.Background(), nil, &containers.Container{}, spec)
	require.NoError(t, err)
	assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{Type: runtimespec.NetworkNamespace, Path: "/proc/1234/ns/net"})
	assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{Type: runtimespec.PIDNamespace, Path: "/proc/1234/ns/pid"})
}

func TestWithPodNamespacesWithPathsHostNetwork(t *testing.T) {
	// Host network/ipc with pid==0 and no explicit should not error and should give host (no pod net/ipc/uts namespace).
	// PID is still required for Pid=NamespaceMode_POD, so provide an explicit PID namespace path when pid==0.
	podConfig := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{
					Network: runtime.NamespaceMode_NODE,
					Pid:     runtime.NamespaceMode_POD,
					Ipc:     runtime.NamespaceMode_NODE,
				},
			},
		},
	}
	containerConfig := &runtime.LinuxContainerSecurityContext{
		NamespaceOptions: &runtime.NamespaceOption{Pid: runtime.NamespaceMode_POD},
	}
	spec := &runtimespec.Spec{Linux: &runtimespec.Linux{}}
	// Provide explicit pid and uts paths for required pod namespaces, but host net/ipc should not require explicit
	err := WithPodNamespacesWithPaths(containerConfig, podConfig, 0, 0, PodNamespacePaths{PID: "/pinned/pid", UTS: "/pinned/uts"}, nil, nil)(context.Background(), nil, &containers.Container{}, spec)
	require.NoError(t, err)
	// Network and IPC should not be added as pod namespaces (host)
	for _, ns := range spec.Linux.Namespaces {
		assert.NotEqual(t, runtimespec.NetworkNamespace, ns.Type, "host network should not add pod network namespace")
		assert.NotEqual(t, runtimespec.IPCNamespace, ns.Type, "host ipc should not add pod ipc namespace")
	}
	assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{Type: runtimespec.PIDNamespace, Path: "/pinned/pid"})
	assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{Type: runtimespec.UTSNamespace, Path: "/pinned/uts"})
}

func TestWithPodNamespacesWithPathsTargetIgnoresPodPidPin(t *testing.T) {
	podConfig := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{
					Network: runtime.NamespaceMode_POD,
					Pid:     runtime.NamespaceMode_POD,
					Ipc:     runtime.NamespaceMode_POD,
				},
			},
		},
	}
	containerConfig := &runtime.LinuxContainerSecurityContext{
		NamespaceOptions: &runtime.NamespaceOption{Pid: runtime.NamespaceMode_TARGET, TargetId: "target-id"},
	}
	// Pod has an explicit PID pin, but container wants TARGET — must use target's pid, not pod's pin.
	spec := &runtimespec.Spec{Linux: &runtimespec.Linux{}}
	err := WithPodNamespacesWithPaths(containerConfig, podConfig, 0, 9999, PodNamespacePaths{Net: "/pinned/net", IPC: "/pinned/ipc", UTS: "/pinned/uts", PID: "/pod/pinned/pid"}, nil, nil)(context.Background(), nil, &containers.Container{}, spec)
	require.NoError(t, err)
	assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{Type: runtimespec.PIDNamespace, Path: "/proc/9999/ns/pid"})
	assert.NotContains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{Type: runtimespec.PIDNamespace, Path: "/pod/pinned/pid"})
}

func TestWithPodNamespacesWithPathsHostPidIgnoresPodPin(t *testing.T) {
	podConfig := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{
					Network: runtime.NamespaceMode_POD,
					Pid:     runtime.NamespaceMode_POD,
					Ipc:     runtime.NamespaceMode_POD,
				},
			},
		},
	}
	containerConfig := &runtime.LinuxContainerSecurityContext{
		NamespaceOptions: &runtime.NamespaceOption{Pid: runtime.NamespaceMode_NODE},
	}
	spec := &runtimespec.Spec{Linux: &runtimespec.Linux{}}
	// Even with a pod PID pin, host PID must be host (no PID namespace).
	err := WithPodNamespacesWithPaths(containerConfig, podConfig, 0, 0, PodNamespacePaths{Net: "/pinned/net", IPC: "/pinned/ipc", UTS: "/pinned/uts", PID: "/pod/pinned/pid"}, nil, nil)(context.Background(), nil, &containers.Container{}, spec)
	require.NoError(t, err)
	for _, ns := range spec.Linux.Namespaces {
		assert.NotEqual(t, runtimespec.PIDNamespace, ns.Type, "host PID should not add pod PID namespace even with pod pin")
	}
	// Network/IPC/UTS should still use explicit pod pins
	assert.Contains(t, spec.Linux.Namespaces, runtimespec.LinuxNamespace{Type: runtimespec.NetworkNamespace, Path: "/pinned/net"})
}
