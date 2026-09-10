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
	"testing"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/pkg/oci"
)

// defaultNamespaces are the namespaces of the default OCI spec: private, no
// path, before the pod namespaces are applied.
func defaultNamespaces() *runtimespec.Spec {
	return &runtimespec.Spec{Linux: &runtimespec.Linux{Namespaces: []runtimespec.LinuxNamespace{
		{Type: runtimespec.PIDNamespace},
		{Type: runtimespec.IPCNamespace},
		{Type: runtimespec.UTSNamespace},
		{Type: runtimespec.MountNamespace},
		{Type: runtimespec.NetworkNamespace},
	}}}
}

func applyNamespaces(t *testing.T, opt oci.SpecOpts) (*runtimespec.Spec, error) {
	t.Helper()
	spec := defaultNamespaces()
	err := opt(context.Background(), nil, nil, spec)
	return spec, err
}

func namespacePaths(spec *runtimespec.Spec) map[runtimespec.LinuxNamespaceType]string {
	paths := make(map[runtimespec.LinuxNamespaceType]string)
	for _, ns := range spec.Linux.Namespaces {
		paths[ns.Type] = ns.Path
	}
	return paths
}

func securityContext(network, ipc, pid runtime.NamespaceMode) *runtime.LinuxContainerSecurityContext {
	return &runtime.LinuxContainerSecurityContext{NamespaceOptions: &runtime.NamespaceOption{
		Network: network,
		Ipc:     ipc,
		Pid:     pid,
	}}
}

func TestWithPodNamespacePathsFromPid(t *testing.T) {
	// A sandbox with a process (pause) keeps today's construction exactly,
	// whatever the requested modes: the container joins the namespaces of the
	// sandbox process, which is itself in the host namespaces the pod asked for.
	for name, sc := range map[string]*runtime.LinuxContainerSecurityContext{
		"pod namespaces":  securityContext(runtime.NamespaceMode_POD, runtime.NamespaceMode_POD, runtime.NamespaceMode_POD),
		"host namespaces": securityContext(runtime.NamespaceMode_NODE, runtime.NamespaceMode_NODE, runtime.NamespaceMode_NODE),
		"container pid":   securityContext(runtime.NamespaceMode_POD, runtime.NamespaceMode_POD, runtime.NamespaceMode_CONTAINER),
	} {
		t.Run(name, func(t *testing.T) {
			legacy, err := applyNamespaces(t, WithPodNamespaces(sc, 42, 42, nil, nil))
			require.NoError(t, err)
			byPath, err := applyNamespaces(t, WithPodNamespacePaths(sc, sc.GetNamespaceOptions(), PodNamespacePathsFromPid(42, 42), nil, nil))
			require.NoError(t, err)
			assert.Equal(t, legacy, byPath)

			paths := namespacePaths(byPath)
			assert.Equal(t, "/proc/42/ns/net", paths[runtimespec.NetworkNamespace])
			assert.Equal(t, "/proc/42/ns/ipc", paths[runtimespec.IPCNamespace])
			assert.Equal(t, "/proc/42/ns/uts", paths[runtimespec.UTSNamespace])
			if sc.GetNamespaceOptions().GetPid() == runtime.NamespaceMode_CONTAINER {
				assert.Equal(t, "", paths[runtimespec.PIDNamespace], "the container keeps its own PID namespace")
			} else {
				assert.Equal(t, "/proc/42/ns/pid", paths[runtimespec.PIDNamespace])
			}
		})
	}

	t.Run("target pid", func(t *testing.T) {
		sc := securityContext(runtime.NamespaceMode_POD, runtime.NamespaceMode_POD, runtime.NamespaceMode_TARGET)
		spec, err := applyNamespaces(t, WithPodNamespacePaths(sc, sc.GetNamespaceOptions(), PodNamespacePathsFromPid(42, 7), nil, nil))
		require.NoError(t, err)
		assert.Equal(t, "/proc/7/ns/pid", namespacePaths(spec)[runtimespec.PIDNamespace])
	})
}

func TestWithPodNamespacePaths(t *testing.T) {
	pathBacked := PodNamespacePaths{
		Network: "/var/run/netns/cni-1",
		IPC:     "/run/sandbox/ns/ipc",
		UTS:     "/run/sandbox/ns/uts",
	}

	t.Run("pod namespaces by path", func(t *testing.T) {
		sc := securityContext(runtime.NamespaceMode_POD, runtime.NamespaceMode_POD, runtime.NamespaceMode_CONTAINER)
		spec, err := applyNamespaces(t, WithPodNamespacePaths(sc, sc.GetNamespaceOptions(), pathBacked, nil, nil))
		require.NoError(t, err)
		assert.Equal(t, map[runtimespec.LinuxNamespaceType]string{
			runtimespec.NetworkNamespace: pathBacked.Network,
			runtimespec.IPCNamespace:     pathBacked.IPC,
			runtimespec.UTSNamespace:     pathBacked.UTS,
			runtimespec.PIDNamespace:     "",
			runtimespec.MountNamespace:   "",
		}, namespacePaths(spec))
	})

	t.Run("host namespaces without paths are removed from the spec", func(t *testing.T) {
		sc := securityContext(runtime.NamespaceMode_NODE, runtime.NamespaceMode_NODE, runtime.NamespaceMode_NODE)
		spec, err := applyNamespaces(t, WithPodNamespacePaths(sc, sc.GetNamespaceOptions(), PodNamespacePaths{}, nil, nil))
		require.NoError(t, err)
		assert.Equal(t, map[runtimespec.LinuxNamespaceType]string{
			runtimespec.MountNamespace: "",
		}, namespacePaths(spec), "only the private mount namespace is left")
	})

	t.Run("a sandbox declaring private PID namespaces", func(t *testing.T) {
		// A pod asking to share its PID namespace on a sandbox that holds
		// none: the sandbox said so explicitly, the container keeps its own.
		sc := securityContext(runtime.NamespaceMode_POD, runtime.NamespaceMode_POD, runtime.NamespaceMode_POD)
		paths := pathBacked
		paths.PrivatePID = true
		spec, err := applyNamespaces(t, WithPodNamespacePaths(sc, sc.GetNamespaceOptions(), paths, nil, nil))
		require.NoError(t, err)
		assert.Equal(t, "", namespacePaths(spec)[runtimespec.PIDNamespace])
		assert.Contains(t, namespacePaths(spec), runtimespec.PIDNamespace)

		// The declaration does not cover the host PID namespace.
		sc = securityContext(runtime.NamespaceMode_POD, runtime.NamespaceMode_POD, runtime.NamespaceMode_NODE)
		spec, err = applyNamespaces(t, WithPodNamespacePaths(sc, sc.GetNamespaceOptions(), paths, nil, nil))
		require.NoError(t, err)
		assert.NotContains(t, namespacePaths(spec), runtimespec.PIDNamespace)
	})

	t.Run("host network with pod ipc", func(t *testing.T) {
		sc := securityContext(runtime.NamespaceMode_NODE, runtime.NamespaceMode_POD, runtime.NamespaceMode_CONTAINER)
		spec, err := applyNamespaces(t, WithPodNamespacePaths(sc, sc.GetNamespaceOptions(), PodNamespacePaths{IPC: pathBacked.IPC}, nil, nil))
		require.NoError(t, err)
		assert.Equal(t, map[runtimespec.LinuxNamespaceType]string{
			runtimespec.IPCNamespace:   pathBacked.IPC,
			runtimespec.PIDNamespace:   "",
			runtimespec.MountNamespace: "",
		}, namespacePaths(spec), "no network and no UTS namespace entry")
	})

	t.Run("the pod decides the host namespaces, not the container config", func(t *testing.T) {
		// A container config that says nothing (NamespaceMode_POD, the zero
		// value) in a host network pod: the container is in the host network
		// and UTS namespaces like the pod, as it was with pause.
		container := securityContext(runtime.NamespaceMode_POD, runtime.NamespaceMode_POD, runtime.NamespaceMode_CONTAINER)
		pod := &runtime.NamespaceOption{Network: runtime.NamespaceMode_NODE, Ipc: runtime.NamespaceMode_POD}
		spec, err := applyNamespaces(t, WithPodNamespacePaths(container, pod, PodNamespacePaths{IPC: pathBacked.IPC}, nil, nil))
		require.NoError(t, err)
		assert.Equal(t, map[runtimespec.LinuxNamespaceType]string{
			runtimespec.IPCNamespace:   pathBacked.IPC,
			runtimespec.PIDNamespace:   "",
			runtimespec.MountNamespace: "",
		}, namespacePaths(spec))
	})

	// A pod namespace the sandbox does not provide is an error, never a
	// silent fallback to a fresh or host namespace.
	for name, tc := range map[string]struct {
		sc    *runtime.LinuxContainerSecurityContext
		paths PodNamespacePaths
	}{
		"missing network": {securityContext(runtime.NamespaceMode_POD, runtime.NamespaceMode_POD, runtime.NamespaceMode_CONTAINER), PodNamespacePaths{IPC: pathBacked.IPC, UTS: pathBacked.UTS}},
		"missing ipc":     {securityContext(runtime.NamespaceMode_POD, runtime.NamespaceMode_POD, runtime.NamespaceMode_CONTAINER), PodNamespacePaths{Network: pathBacked.Network, UTS: pathBacked.UTS}},
		"missing uts":     {securityContext(runtime.NamespaceMode_POD, runtime.NamespaceMode_POD, runtime.NamespaceMode_CONTAINER), PodNamespacePaths{Network: pathBacked.Network, IPC: pathBacked.IPC}},
		"missing pid":     {securityContext(runtime.NamespaceMode_POD, runtime.NamespaceMode_POD, runtime.NamespaceMode_POD), pathBacked},
		"missing user": {&runtime.LinuxContainerSecurityContext{NamespaceOptions: &runtime.NamespaceOption{
			Network:       runtime.NamespaceMode_POD,
			Ipc:           runtime.NamespaceMode_POD,
			Pid:           runtime.NamespaceMode_CONTAINER,
			UsernsOptions: &runtime.UserNamespace{Mode: runtime.NamespaceMode_POD},
		}}, pathBacked},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := applyNamespaces(t, WithPodNamespacePaths(tc.sc, tc.sc.GetNamespaceOptions(), tc.paths, nil, nil))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "provides no")
		})
	}

	t.Run("pod user namespace by path", func(t *testing.T) {
		sc := &runtime.LinuxContainerSecurityContext{NamespaceOptions: &runtime.NamespaceOption{
			Network:       runtime.NamespaceMode_POD,
			Ipc:           runtime.NamespaceMode_POD,
			Pid:           runtime.NamespaceMode_CONTAINER,
			UsernsOptions: &runtime.UserNamespace{Mode: runtime.NamespaceMode_POD},
		}}
		paths := pathBacked
		paths.User = "/run/sandbox/ns/user"
		uids := []runtimespec.LinuxIDMapping{{ContainerID: 0, HostID: 65536, Size: 65536}}
		spec, err := applyNamespaces(t, WithPodNamespacePaths(sc, sc.GetNamespaceOptions(), paths, uids, uids))
		require.NoError(t, err)
		assert.Equal(t, paths.User, namespacePaths(spec)[runtimespec.UserNamespace])
		assert.Equal(t, uids, spec.Linux.UIDMappings)
		assert.Equal(t, uids, spec.Linux.GIDMappings)
	})
}
