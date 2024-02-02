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
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/cri/store/container"
	typeurl "github.com/containerd/typeurl/v2"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToCRIContainerUser(t *testing.T) {
	testCases := []struct {
		name     string
		spec     *specs.Spec
		expected *runtime.ContainerUser
	}{
		{
			name:     "no Process",
			spec:     &specs.Spec{},
			expected: nil,
		},
		{
			name: "no additionalGids",
			spec: &specs.Spec{
				Process: &specs.Process{
					User: specs.User{
						UID: 0,
						GID: 0,
					},
				},
			},
			expected: &runtime.ContainerUser{
				Linux: &runtime.LinuxContainerUser{
					Uid: 0,
					Gid: 0,
				},
			},
		},
		{
			name: "with additionalGids",
			spec: &specs.Spec{
				Process: &specs.Process{
					User: specs.User{
						UID:            0,
						GID:            0,
						AdditionalGids: []uint32{0, 1234},
					},
				},
			},
			expected: &runtime.ContainerUser{
				Linux: &runtime.LinuxContainerUser{
					Uid:                0,
					Gid:                0,
					SupplementalGroups: []int64{0, 1234},
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := toCRIContainerUser(context.Background(), container.Container{
				Container: &fakeSpecOnlyContainer{
					spec: testCase.spec,
				},
			})
			require.NoError(t, err)
			assert.Equal(t, got, testCase.expected)
		})
	}
}

var _ containerd.Container = &fakeSpecOnlyContainer{}

type fakeSpecOnlyContainer struct {
	spec *specs.Spec
}

// Spec implements client.Container.
func (c *fakeSpecOnlyContainer) Spec(context.Context) (*specs.Spec, error) {
	return c.spec, nil
}

// Checkpoint implements client.Container.
func (*fakeSpecOnlyContainer) Checkpoint(context.Context, string, ...containerd.CheckpointOpts) (containerd.Image, error) {
	panic("unimplemented")
}

// Delete implements client.Container.
func (*fakeSpecOnlyContainer) Delete(context.Context, ...containerd.DeleteOpts) error {
	panic("unimplemented")
}

// Extensions implements client.Container.
func (*fakeSpecOnlyContainer) Extensions(context.Context) (map[string]typeurl.Any, error) {
	panic("unimplemented")
}

// ID implements client.Container.
func (*fakeSpecOnlyContainer) ID() string {
	panic("unimplemented")
}

// Image implements client.Container.
func (*fakeSpecOnlyContainer) Image(context.Context) (containerd.Image, error) {
	panic("unimplemented")
}

// Info implements client.Container.
func (*fakeSpecOnlyContainer) Info(context.Context, ...containerd.InfoOpts) (containers.Container, error) {
	panic("unimplemented")
}

// Labels implements client.Container.
func (*fakeSpecOnlyContainer) Labels(context.Context) (map[string]string, error) {
	panic("unimplemented")
}

// NewTask implements client.Container.
func (*fakeSpecOnlyContainer) NewTask(context.Context, cio.Creator, ...containerd.NewTaskOpts) (containerd.Task, error) {
	panic("unimplemented")
}

// SetLabels implements client.Container.
func (*fakeSpecOnlyContainer) SetLabels(context.Context, map[string]string) (map[string]string, error) {
	panic("unimplemented")
}

// Task implements client.Container.
func (*fakeSpecOnlyContainer) Task(context.Context, cio.Attach) (containerd.Task, error) {
	panic("unimplemented")
}

// Update implements client.Container.
func (*fakeSpecOnlyContainer) Update(context.Context, ...containerd.UpdateContainerOpts) error {
	panic("unimplemented")
}
