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

package podsandbox

import (
	goruntime "runtime"
	"testing"

	"github.com/containerd/typeurl/v2"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	sandboxstore "github.com/containerd/containerd/v2/pkg/cri/store/sandbox"
)

func TestSandboxContainerSpec(t *testing.T) {
	switch goruntime.GOOS {
	case "darwin":
		t.Skip("not implemented on Darwin")
	case "freebsd":
		t.Skip("not implemented on FreeBSD")
	}
	testID := "test-id"
	nsPath := "test-cni"
	for _, test := range []struct {
		desc              string
		configChange      func(*runtime.PodSandboxConfig)
		podAnnotations    []string
		imageConfigChange func(*imagespec.ImageConfig)
		specCheck         func(*testing.T, *runtimespec.Spec)
		expectErr         bool
	}{
		{
			desc: "should return error when entrypoint and cmd are empty",
			imageConfigChange: func(c *imagespec.ImageConfig) {
				c.Entrypoint = nil
				c.Cmd = nil
			},
			expectErr: true,
		},
		{
			desc:           "a passthrough annotation should be passed as an OCI annotation",
			podAnnotations: []string{"c"},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				assert.Equal(t, spec.Annotations["c"], "d")
			},
		},
		{
			desc: "a non-passthrough annotation should not be passed as an OCI annotation",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Annotations["d"] = "e"
			},
			podAnnotations: []string{"c"},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				assert.Equal(t, spec.Annotations["c"], "d")
				_, ok := spec.Annotations["d"]
				assert.False(t, ok)
			},
		},
		{
			desc: "passthrough annotations should support wildcard match",
			configChange: func(c *runtime.PodSandboxConfig) {
				c.Annotations["t.f"] = "j"
				c.Annotations["z.g"] = "o"
				c.Annotations["z"] = "o"
				c.Annotations["y.ca"] = "b"
				c.Annotations["y"] = "b"
			},
			podAnnotations: []string{"t*", "z.*", "y.c*"},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				assert.Equal(t, spec.Annotations["t.f"], "j")
				assert.Equal(t, spec.Annotations["z.g"], "o")
				assert.Equal(t, spec.Annotations["y.ca"], "b")
				_, ok := spec.Annotations["y"]
				assert.False(t, ok)
				_, ok = spec.Annotations["z"]
				assert.False(t, ok)
			},
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			c := newControllerService()
			config, imageConfig, specCheck := getRunPodSandboxTestData()
			if test.configChange != nil {
				test.configChange(config)
			}

			if test.imageConfigChange != nil {
				test.imageConfigChange(imageConfig)
			}
			spec, err := c.sandboxContainerSpec(testID, config, imageConfig, nsPath,
				test.podAnnotations)
			if test.expectErr {
				assert.Error(t, err)
				assert.Nil(t, spec)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, spec)
			specCheck(t, testID, spec)
			if test.specCheck != nil {
				test.specCheck(t, spec)
			}
		})
	}
}

func TestTypeurlMarshalUnmarshalSandboxMeta(t *testing.T) {
	for _, test := range []struct {
		desc         string
		configChange func(*runtime.PodSandboxConfig)
	}{
		{
			desc: "should marshal original config",
		},
		{
			desc: "should marshal Linux",
			configChange: func(c *runtime.PodSandboxConfig) {
				if c.Linux == nil {
					c.Linux = &runtime.LinuxPodSandboxConfig{}
				}
				c.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{
					NamespaceOptions: &runtime.NamespaceOption{
						Network: runtime.NamespaceMode_NODE,
						Pid:     runtime.NamespaceMode_NODE,
						Ipc:     runtime.NamespaceMode_NODE,
					},
					SupplementalGroups: []int64{1111, 2222},
				}
			},
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			meta := &sandboxstore.Metadata{
				ID:        "1",
				Name:      "sandbox_1",
				NetNSPath: "/home/cloud",
			}
			meta.Config, _, _ = getRunPodSandboxTestData()
			if test.configChange != nil {
				test.configChange(meta.Config)
			}

			md, err := typeurl.MarshalAny(meta)
			assert.NoError(t, err)
			data, err := typeurl.UnmarshalAny(md)
			assert.NoError(t, err)
			assert.IsType(t, &sandboxstore.Metadata{}, data)
			curMeta, ok := data.(*sandboxstore.Metadata)
			assert.True(t, ok)
			assert.Equal(t, meta, curMeta)
		})
	}
}
