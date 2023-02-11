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

	"github.com/containerd/containerd/pkg/cri/annotations"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
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
	for desc, test := range map[string]struct {
		configChange      func(*runtime.PodSandboxConfig)
		podAnnotations    []string
		imageConfigChange func(*imagespec.ImageConfig)
		specCheck         func(*testing.T, *runtimespec.Spec)
		expectErr         bool
	}{
		"should return error when entrypoint and cmd are empty": {
			imageConfigChange: func(c *imagespec.ImageConfig) {
				c.Entrypoint = nil
				c.Cmd = nil
			},
			expectErr: true,
		},
		"a passthrough annotation should be passed as an OCI annotation": {
			podAnnotations: []string{"c"},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				assert.Equal(t, spec.Annotations["c"], "d")
			},
		},
		"a non-passthrough annotation should not be passed as an OCI annotation": {
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
		"passthrough annotations should support wildcard match": {
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
		t.Run(desc, func(t *testing.T) {
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
	for desc, test := range map[string]struct {
		configChange func(*runtime.PodSandboxConfig)
	}{
		"should marshal original config": {},
		"should marshal Linux": {
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
		t.Run(desc, func(t *testing.T) {
			meta := &sandboxstore.Metadata{
				ID:        "1",
				Name:      "sandbox_1",
				NetNSPath: "/home/cloud",
			}
			meta.Config, _, _ = getRunPodSandboxTestData()
			if test.configChange != nil {
				test.configChange(meta.Config)
			}

			any, err := typeurl.MarshalAny(meta)
			assert.NoError(t, err)
			data, err := typeurl.UnmarshalAny(any)
			assert.NoError(t, err)
			assert.IsType(t, &sandboxstore.Metadata{}, data)
			curMeta, ok := data.(*sandboxstore.Metadata)
			assert.True(t, ok)
			assert.Equal(t, meta, curMeta)
		})
	}
}

func TestHostAccessingSandbox(t *testing.T) {
	privilegedContext := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				Privileged: true,
			},
		},
	}
	nonPrivilegedContext := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				Privileged: false,
			},
		},
	}
	hostNamespace := &runtime.PodSandboxConfig{
		Linux: &runtime.LinuxPodSandboxConfig{
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				Privileged: false,
				NamespaceOptions: &runtime.NamespaceOption{
					Network: runtime.NamespaceMode_NODE,
					Pid:     runtime.NamespaceMode_NODE,
					Ipc:     runtime.NamespaceMode_NODE,
				},
			},
		},
	}
	tests := []struct {
		name   string
		config *runtime.PodSandboxConfig
		want   bool
	}{
		{"Security Context is nil", nil, false},
		{"Security Context is privileged", privilegedContext, false},
		{"Security Context is not privileged", nonPrivilegedContext, false},
		{"Security Context namespace host access", hostNamespace, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hostAccessingSandbox(tt.config); got != tt.want {
				t.Errorf("hostAccessingSandbox() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetSandboxRuntime(t *testing.T) {
	untrustedWorkloadRuntime := criconfig.Runtime{
		Type:   "io.containerd.runtime.v1.linux",
		Engine: "untrusted-workload-runtime",
		Root:   "",
	}

	defaultRuntime := criconfig.Runtime{
		Type:   "io.containerd.runtime.v1.linux",
		Engine: "default-runtime",
		Root:   "",
	}

	fooRuntime := criconfig.Runtime{
		Type:   "io.containerd.runtime.v1.linux",
		Engine: "foo-bar",
		Root:   "",
	}

	for desc, test := range map[string]struct {
		sandboxConfig   *runtime.PodSandboxConfig
		runtimeHandler  string
		runtimes        map[string]criconfig.Runtime
		expectErr       bool
		expectedRuntime criconfig.Runtime
	}{
		"should return error if untrusted workload requires host access": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Linux: &runtime.LinuxPodSandboxConfig{
					SecurityContext: &runtime.LinuxSandboxSecurityContext{
						Privileged: false,
						NamespaceOptions: &runtime.NamespaceOption{
							Network: runtime.NamespaceMode_NODE,
							Pid:     runtime.NamespaceMode_NODE,
							Ipc:     runtime.NamespaceMode_NODE,
						},
					},
				},
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault:   defaultRuntime,
				criconfig.RuntimeUntrusted: untrustedWorkloadRuntime,
			},
			expectErr: true,
		},
		"should use untrusted workload runtime for untrusted workload": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault:   defaultRuntime,
				criconfig.RuntimeUntrusted: untrustedWorkloadRuntime,
			},
			expectedRuntime: untrustedWorkloadRuntime,
		},
		"should use default runtime for regular workload": {
			sandboxConfig: &runtime.PodSandboxConfig{},
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault: defaultRuntime,
			},
			expectedRuntime: defaultRuntime,
		},
		"should use default runtime for trusted workload": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "false",
				},
			},
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault:   defaultRuntime,
				criconfig.RuntimeUntrusted: untrustedWorkloadRuntime,
			},
			expectedRuntime: defaultRuntime,
		},
		"should return error if untrusted workload runtime is required but not configured": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault: defaultRuntime,
			},
			expectErr: true,
		},
		"should use 'untrusted' runtime for untrusted workload": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault:   defaultRuntime,
				criconfig.RuntimeUntrusted: untrustedWorkloadRuntime,
			},
			expectedRuntime: untrustedWorkloadRuntime,
		},
		"should use 'untrusted' runtime for untrusted workload & handler": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			runtimeHandler: "untrusted",
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault:   defaultRuntime,
				criconfig.RuntimeUntrusted: untrustedWorkloadRuntime,
			},
			expectedRuntime: untrustedWorkloadRuntime,
		},
		"should return an error if untrusted annotation with conflicting handler": {
			sandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.UntrustedWorkload: "true",
				},
			},
			runtimeHandler: "foo",
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault:   defaultRuntime,
				criconfig.RuntimeUntrusted: untrustedWorkloadRuntime,
				"foo":                      fooRuntime,
			},
			expectErr: true,
		},
		"should use correct runtime for a runtime handler": {
			sandboxConfig:  &runtime.PodSandboxConfig{},
			runtimeHandler: "foo",
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault:   defaultRuntime,
				criconfig.RuntimeUntrusted: untrustedWorkloadRuntime,
				"foo":                      fooRuntime,
			},
			expectedRuntime: fooRuntime,
		},
		"should return error if runtime handler is required but not configured": {
			sandboxConfig:  &runtime.PodSandboxConfig{},
			runtimeHandler: "bar",
			runtimes: map[string]criconfig.Runtime{
				criconfig.RuntimeDefault: defaultRuntime,
				"foo":                    fooRuntime,
			},
			expectErr: true,
		},
	} {
		t.Run(desc, func(t *testing.T) {
			cri := newControllerService()
			cri.config = criconfig.Config{
				PluginConfig: criconfig.DefaultConfig(),
			}
			cri.config.ContainerdConfig.DefaultRuntimeName = criconfig.RuntimeDefault
			cri.config.ContainerdConfig.Runtimes = test.runtimes
			r, err := cri.getSandboxRuntime(test.sandboxConfig, test.runtimeHandler)
			assert.Equal(t, test.expectErr, err != nil)
			assert.Equal(t, test.expectedRuntime, r)
		})
	}
}
