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

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestAmbientCapabilitiesReplaceDefaults(t *testing.T) {
	for _, sc := range []*runtime.LinuxContainerSecurityContext{nil, {}, {Capabilities: &runtime.Capability{AddCapabilities: []string{"CHOWN"}}}} {
		spec := &runtimespec.Spec{Process: &runtimespec.Process{Capabilities: &runtimespec.LinuxCapabilities{Ambient: []string{"CAP_SYS_ADMIN"}}}}
		require.NoError(t, WithAmbientCapabilities(sc, nil)(context.Background(), nil, nil, spec))
		assert.Empty(t, spec.Process.Capabilities.Ambient)
	}
}

func TestAmbientCapabilitiesPreserveInheritable(t *testing.T) {
	spec := &runtimespec.Spec{Process: &runtimespec.Process{Capabilities: &runtimespec.LinuxCapabilities{Inheritable: []string{"CAP_CHOWN"}}}}
	sc := &runtime.LinuxContainerSecurityContext{Capabilities: &runtime.Capability{AddAmbientCapabilities: []string{"NET_BIND_SERVICE"}}}
	require.NoError(t, WithAmbientCapabilities(sc, []string{"CAP_NET_BIND_SERVICE"})(context.Background(), nil, nil, spec))
	assert.Equal(t, []string{"CAP_CHOWN", "CAP_NET_BIND_SERVICE"}, spec.Process.Capabilities.Inheritable)
	assert.Equal(t, []string{"CAP_NET_BIND_SERVICE"}, spec.Process.Capabilities.Ambient)
}

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
