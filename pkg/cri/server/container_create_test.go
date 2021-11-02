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
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/containerd/containerd/oci"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/constants"
	"github.com/containerd/containerd/pkg/cri/opts"
)

func checkMount(t *testing.T, mounts []runtimespec.Mount, src, dest, typ string,
	contains, notcontains []string) {
	found := false
	for _, m := range mounts {
		if m.Source == src && m.Destination == dest {
			assert.Equal(t, m.Type, typ)
			for _, c := range contains {
				assert.Contains(t, m.Options, c)
			}
			for _, n := range notcontains {
				assert.NotContains(t, m.Options, n)
			}
			found = true
			break
		}
	}
	assert.True(t, found, "mount from %q to %q not found", src, dest)
}

const testImageName = "container-image-name"

func TestGeneralContainerSpec(t *testing.T) {
	testID := "test-id"
	testPid := uint32(1234)
	containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
	ociRuntime := config.Runtime{}
	c := newTestCRIService()
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	spec, err := c.containerSpec(testID, testSandboxID, testPid, "", testContainerName, testImageName, containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
	require.NoError(t, err)
	specCheck(t, testID, testSandboxID, testPid, spec)
}

func TestPodAnnotationPassthroughContainerSpec(t *testing.T) {
	if goruntime.GOOS == "darwin" {
		t.Skip("not implemented on Darwin")
	}

	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)

	for desc, test := range map[string]struct {
		podAnnotations []string
		configChange   func(*runtime.PodSandboxConfig)
		specCheck      func(*testing.T, *runtimespec.Spec)
	}{
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
				t.Logf("%+v", spec.Annotations)
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
			c := newTestCRIService()
			containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
			if test.configChange != nil {
				test.configChange(sandboxConfig)
			}

			ociRuntime := config.Runtime{
				PodAnnotations: test.podAnnotations,
			}
			spec, err := c.containerSpec(testID, testSandboxID, testPid, "", testContainerName, testImageName,
				containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
			assert.NoError(t, err)
			assert.NotNil(t, spec)
			specCheck(t, testID, testSandboxID, testPid, spec)
			if test.specCheck != nil {
				test.specCheck(t, spec)
			}
		})
	}
}

func TestContainerSpecCommand(t *testing.T) {
	for desc, test := range map[string]struct {
		criEntrypoint   []string
		criArgs         []string
		imageEntrypoint []string
		imageArgs       []string
		expected        []string
		expectErr       bool
	}{
		"should use cri entrypoint if it's specified": {
			criEntrypoint:   []string{"a", "b"},
			imageEntrypoint: []string{"c", "d"},
			imageArgs:       []string{"e", "f"},
			expected:        []string{"a", "b"},
		},
		"should use cri entrypoint if it's specified even if it's empty": {
			criEntrypoint:   []string{},
			criArgs:         []string{"a", "b"},
			imageEntrypoint: []string{"c", "d"},
			imageArgs:       []string{"e", "f"},
			expected:        []string{"a", "b"},
		},
		"should use cri entrypoint and args if they are specified": {
			criEntrypoint:   []string{"a", "b"},
			criArgs:         []string{"c", "d"},
			imageEntrypoint: []string{"e", "f"},
			imageArgs:       []string{"g", "h"},
			expected:        []string{"a", "b", "c", "d"},
		},
		"should use image entrypoint if cri entrypoint is not specified": {
			criArgs:         []string{"a", "b"},
			imageEntrypoint: []string{"c", "d"},
			imageArgs:       []string{"e", "f"},
			expected:        []string{"c", "d", "a", "b"},
		},
		"should use image args if both cri entrypoint and args are not specified": {
			imageEntrypoint: []string{"c", "d"},
			imageArgs:       []string{"e", "f"},
			expected:        []string{"c", "d", "e", "f"},
		},
		"should return error if both entrypoint and args are empty": {
			expectErr: true,
		},
	} {

		config, _, imageConfig, _ := getCreateContainerTestData()
		config.Command = test.criEntrypoint
		config.Args = test.criArgs
		imageConfig.Entrypoint = test.imageEntrypoint
		imageConfig.Cmd = test.imageArgs

		var spec runtimespec.Spec
		err := opts.WithProcessArgs(config, imageConfig)(context.Background(), nil, nil, &spec)
		if test.expectErr {
			assert.Error(t, err)
			continue
		}
		assert.NoError(t, err)
		assert.Equal(t, test.expected, spec.Process.Args, desc)
	}
}

func TestVolumeMounts(t *testing.T) {
	testContainerRootDir := "test-container-root"
	for desc, test := range map[string]struct {
		criMounts         []*runtime.Mount
		imageVolumes      map[string]struct{}
		expectedMountDest []string
	}{
		"should setup rw mount for image volumes": {
			imageVolumes: map[string]struct{}{
				"/test-volume-1": {},
				"/test-volume-2": {},
			},
			expectedMountDest: []string{
				"/test-volume-1",
				"/test-volume-2",
			},
		},
		"should skip image volumes if already mounted by CRI": {
			criMounts: []*runtime.Mount{
				{
					ContainerPath: "/test-volume-1",
					HostPath:      "/test-hostpath-1",
				},
			},
			imageVolumes: map[string]struct{}{
				"/test-volume-1": {},
				"/test-volume-2": {},
			},
			expectedMountDest: []string{
				"/test-volume-2",
			},
		},
		"should compare and return cleanpath": {
			criMounts: []*runtime.Mount{
				{
					ContainerPath: "/test-volume-1",
					HostPath:      "/test-hostpath-1",
				},
			},
			imageVolumes: map[string]struct{}{
				"/test-volume-1/": {},
				"/test-volume-2/": {},
			},
			expectedMountDest: []string{
				"/test-volume-2/",
			},
		},
	} {
		t.Logf("TestCase %q", desc)
		config := &imagespec.ImageConfig{
			Volumes: test.imageVolumes,
		}
		c := newTestCRIService()
		got := c.volumeMounts(testContainerRootDir, test.criMounts, config)
		assert.Len(t, got, len(test.expectedMountDest))
		for _, dest := range test.expectedMountDest {
			found := false
			for _, m := range got {
				if m.ContainerPath == dest {
					found = true
					assert.Equal(t,
						filepath.Dir(m.HostPath),
						filepath.Join(testContainerRootDir, "volumes"))
					break
				}
			}
			assert.True(t, found)
		}
	}
}

func TestContainerAnnotationPassthroughContainerSpec(t *testing.T) {
	if goruntime.GOOS == "darwin" {
		t.Skip("not implemented on Darwin")
	}

	testID := "test-id"
	testSandboxID := "sandbox-id"
	testContainerName := "container-name"
	testPid := uint32(1234)

	for desc, test := range map[string]struct {
		podAnnotations       []string
		containerAnnotations []string
		podConfigChange      func(*runtime.PodSandboxConfig)
		configChange         func(*runtime.ContainerConfig)
		specCheck            func(*testing.T, *runtimespec.Spec)
	}{
		"passthrough annotations from pod and container should be passed as an OCI annotation": {
			podConfigChange: func(p *runtime.PodSandboxConfig) {
				p.Annotations["pod.annotation.1"] = "1"
				p.Annotations["pod.annotation.2"] = "2"
				p.Annotations["pod.annotation.3"] = "3"
			},
			configChange: func(c *runtime.ContainerConfig) {
				c.Annotations["container.annotation.1"] = "1"
				c.Annotations["container.annotation.2"] = "2"
				c.Annotations["container.annotation.3"] = "3"
			},
			podAnnotations:       []string{"pod.annotation.1"},
			containerAnnotations: []string{"container.annotation.1"},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				assert.Equal(t, "1", spec.Annotations["container.annotation.1"])
				_, ok := spec.Annotations["container.annotation.2"]
				assert.False(t, ok)
				_, ok = spec.Annotations["container.annotation.3"]
				assert.False(t, ok)
				assert.Equal(t, "1", spec.Annotations["pod.annotation.1"])
				_, ok = spec.Annotations["pod.annotation.2"]
				assert.False(t, ok)
				_, ok = spec.Annotations["pod.annotation.3"]
				assert.False(t, ok)
			},
		},
		"passthrough annotations from pod and container should support wildcard": {
			podConfigChange: func(p *runtime.PodSandboxConfig) {
				p.Annotations["pod.annotation.1"] = "1"
				p.Annotations["pod.annotation.2"] = "2"
				p.Annotations["pod.annotation.3"] = "3"
			},
			configChange: func(c *runtime.ContainerConfig) {
				c.Annotations["container.annotation.1"] = "1"
				c.Annotations["container.annotation.2"] = "2"
				c.Annotations["container.annotation.3"] = "3"
			},
			podAnnotations:       []string{"pod.annotation.*"},
			containerAnnotations: []string{"container.annotation.*"},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				assert.Equal(t, "1", spec.Annotations["container.annotation.1"])
				assert.Equal(t, "2", spec.Annotations["container.annotation.2"])
				assert.Equal(t, "3", spec.Annotations["container.annotation.3"])
				assert.Equal(t, "1", spec.Annotations["pod.annotation.1"])
				assert.Equal(t, "2", spec.Annotations["pod.annotation.2"])
				assert.Equal(t, "3", spec.Annotations["pod.annotation.3"])
			},
		},
		"annotations should not pass through if no passthrough annotations are configured": {
			podConfigChange: func(p *runtime.PodSandboxConfig) {
				p.Annotations["pod.annotation.1"] = "1"
				p.Annotations["pod.annotation.2"] = "2"
				p.Annotations["pod.annotation.3"] = "3"
			},
			configChange: func(c *runtime.ContainerConfig) {
				c.Annotations["container.annotation.1"] = "1"
				c.Annotations["container.annotation.2"] = "2"
				c.Annotations["container.annotation.3"] = "3"
			},
			podAnnotations:       []string{},
			containerAnnotations: []string{},
			specCheck: func(t *testing.T, spec *runtimespec.Spec) {
				_, ok := spec.Annotations["container.annotation.1"]
				assert.False(t, ok)
				_, ok = spec.Annotations["container.annotation.2"]
				assert.False(t, ok)
				_, ok = spec.Annotations["container.annotation.3"]
				assert.False(t, ok)
				_, ok = spec.Annotations["pod.annotation.1"]
				assert.False(t, ok)
				_, ok = spec.Annotations["pod.annotation.2"]
				assert.False(t, ok)
				_, ok = spec.Annotations["pod.annotation.3"]
				assert.False(t, ok)
			},
		},
	} {
		t.Run(desc, func(t *testing.T) {
			c := newTestCRIService()
			containerConfig, sandboxConfig, imageConfig, specCheck := getCreateContainerTestData()
			if test.configChange != nil {
				test.configChange(containerConfig)
			}
			if test.podConfigChange != nil {
				test.podConfigChange(sandboxConfig)
			}
			ociRuntime := config.Runtime{
				PodAnnotations:       test.podAnnotations,
				ContainerAnnotations: test.containerAnnotations,
			}
			spec, err := c.containerSpec(testID, testSandboxID, testPid, "", testContainerName, testImageName,
				containerConfig, sandboxConfig, imageConfig, nil, ociRuntime)
			assert.NoError(t, err)
			assert.NotNil(t, spec)
			specCheck(t, testID, testSandboxID, testPid, spec)
			if test.specCheck != nil {
				test.specCheck(t, spec)
			}
		})
	}
}

func TestBaseRuntimeSpec(t *testing.T) {
	c := newTestCRIService()
	c.baseOCISpecs = map[string]*oci.Spec{
		"/etc/containerd/cri-base.json": {
			Version:  "1.0.2",
			Hostname: "old",
		},
	}

	out, err := c.runtimeSpec("id1", "/etc/containerd/cri-base.json", oci.WithHostname("new"))
	assert.NoError(t, err)

	assert.Equal(t, "1.0.2", out.Version)
	assert.Equal(t, "new", out.Hostname)

	// Make sure original base spec not changed
	assert.NotEqual(t, out, c.baseOCISpecs["/etc/containerd/cri-base.json"])
	assert.Equal(t, c.baseOCISpecs["/etc/containerd/cri-base.json"].Hostname, "old")

	assert.Equal(t, filepath.Join("/", constants.K8sContainerdNamespace, "id1"), out.Linux.CgroupsPath)
}
