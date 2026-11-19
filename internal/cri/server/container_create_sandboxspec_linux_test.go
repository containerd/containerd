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
	"os"
	"testing"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	customopts "github.com/containerd/containerd/v2/internal/cri/opts"
	ostesting "github.com/containerd/containerd/v2/pkg/os/testing"
)

// pathBackedSandboxSpec is what a sandbox without a process returns on start:
// the namespaces it holds and the pod shared files it owns.
func pathBackedSandboxSpec() *runtimespec.Spec {
	return &runtimespec.Spec{
		Linux: &runtimespec.Linux{
			Namespaces: []runtimespec.LinuxNamespace{
				{Type: runtimespec.NetworkNamespace, Path: "/var/run/netns/cni-1"},
				{Type: runtimespec.IPCNamespace, Path: "/run/sandbox/ns/ipc"},
				{Type: runtimespec.UTSNamespace, Path: "/run/sandbox/ns/uts"},
			},
		},
		Mounts: []runtimespec.Mount{
			{Destination: "/etc/hostname", Type: "bind", Source: "/run/sandbox/hostname"},
			{Destination: "/etc/hosts", Type: "bind", Source: "/run/sandbox/hosts"},
			{Destination: "/etc/resolv.conf", Type: "bind", Source: "/run/sandbox/resolv.conf"},
			{Destination: "/dev/shm", Type: "bind", Source: "/run/sandbox/shm"},
		},
	}
}

func TestPodNamespacePaths(t *testing.T) {
	spec := pathBackedSandboxSpec()
	for name, tc := range map[string]struct {
		sandboxPid, targetPid uint32
		spec                  *runtimespec.Spec
		expected              customopts.PodNamespacePaths
	}{
		"a sandbox process wins over the spec": {
			sandboxPid: 42, targetPid: 42, spec: spec,
			expected: customopts.PodNamespacePathsFromPid(42, 42),
		},
		"no process and no spec is a legacy sandbox api shim": {
			expected: customopts.PodNamespacePathsFromPid(0, 0),
		},
		"no process and a spec without a linux section is legacy too": {
			spec:     &runtimespec.Spec{},
			expected: customopts.PodNamespacePathsFromPid(0, 0),
		},
		"no process and a spec with namespaces is path backed": {
			spec: spec,
			expected: customopts.PodNamespacePaths{
				Network: "/var/run/netns/cni-1",
				IPC:     "/run/sandbox/ns/ipc",
				UTS:     "/run/sandbox/ns/uts",
			},
		},
		"a path backed sandbox in the host namespaces provides none": {
			spec:     &runtimespec.Spec{Linux: &runtimespec.Linux{}},
			expected: customopts.PodNamespacePaths{},
		},
		"a pid namespace entry without a path declares private pid namespaces": {
			spec: &runtimespec.Spec{Linux: &runtimespec.Linux{Namespaces: []runtimespec.LinuxNamespace{
				{Type: runtimespec.NetworkNamespace, Path: "/var/run/netns/cni-1"},
				{Type: runtimespec.PIDNamespace},
			}}},
			expected: customopts.PodNamespacePaths{Network: "/var/run/netns/cni-1", PrivatePID: true},
		},
		"a target container pid namespace is joined by pid": {
			targetPid: 7, spec: spec,
			expected: customopts.PodNamespacePaths{
				Network: "/var/run/netns/cni-1",
				IPC:     "/run/sandbox/ns/ipc",
				UTS:     "/run/sandbox/ns/uts",
				PID:     "/proc/7/ns/pid",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.expected, podNamespacePaths(tc.sandboxPid, tc.targetPid, tc.spec))
		})
	}
}

func TestLinuxContainerMountsFromSandboxSpec(t *testing.T) {
	const testSandboxID = "test-sandbox-id"
	config := &runtime.ContainerConfig{
		Metadata: &runtime.ContainerMetadata{Name: "test-name", Attempt: 1},
		Linux: &runtime.LinuxContainerConfig{
			SecurityContext: &runtime.LinuxContainerSecurityContext{},
		},
	}
	exists := func(paths ...string) func(string) (os.FileInfo, error) {
		set := make(map[string]struct{}, len(paths))
		for _, p := range paths {
			set[p] = struct{}{}
		}
		return func(p string) (os.FileInfo, error) {
			if _, ok := set[p]; ok {
				return nil, nil
			}
			return nil, os.ErrNotExist
		}
	}
	sharedMount := func(containerPath, hostPath string) *runtime.Mount {
		return &runtime.Mount{ContainerPath: containerPath, HostPath: hostPath, SelinuxRelabel: true}
	}

	t.Run("the sandbox spec provides the pod shared files", func(t *testing.T) {
		c := newTestCRIService()
		c.os.(*ostesting.FakeOS).StatFn = exists("/run/sandbox/hostname", "/run/sandbox/hosts", "/run/sandbox/resolv.conf", "/run/sandbox/shm")
		assert.Equal(t, []*runtime.Mount{
			sharedMount(etcHostname, "/run/sandbox/hostname"),
			sharedMount(etcHosts, "/run/sandbox/hosts"),
			sharedMount(resolvConfPath, "/run/sandbox/resolv.conf"),
			sharedMount(devShm, "/run/sandbox/shm"),
		}, c.linuxContainerMounts(testSandboxID, config, pathBackedSandboxSpec()))
	})

	t.Run("the fixed sandbox directory wins over the spec", func(t *testing.T) {
		c := newTestCRIService()
		c.os.(*ostesting.FakeOS).StatFn = exists(c.getSandboxHosts(testSandboxID), "/run/sandbox/hosts")
		assert.Equal(t, []*runtime.Mount{
			sharedMount(etcHosts, c.getSandboxHosts(testSandboxID)),
		}, c.linuxContainerMounts(testSandboxID, config, pathBackedSandboxSpec()))
	})

	t.Run("a spec source that does not exist is not mounted", func(t *testing.T) {
		c := newTestCRIService()
		c.os.(*ostesting.FakeOS).StatFn = exists()
		assert.Empty(t, c.linuxContainerMounts(testSandboxID, config, pathBackedSandboxSpec()))
	})

	t.Run("host ipc uses the host shm", func(t *testing.T) {
		c := newTestCRIService()
		c.os.(*ostesting.FakeOS).StatFn = exists(devShm, "/run/sandbox/shm")
		hostIPC := &runtime.ContainerConfig{
			Metadata: config.Metadata,
			Linux: &runtime.LinuxContainerConfig{
				SecurityContext: &runtime.LinuxContainerSecurityContext{
					NamespaceOptions: &runtime.NamespaceOption{Ipc: runtime.NamespaceMode_NODE},
				},
			},
		}
		assert.Equal(t, []*runtime.Mount{
			{ContainerPath: devShm, HostPath: devShm, SelinuxRelabel: false},
		}, c.linuxContainerMounts(testSandboxID, hostIPC, pathBackedSandboxSpec()))
	})
}
