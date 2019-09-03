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
	"path/filepath"
	"testing"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

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
