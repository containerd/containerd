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
	"errors"
	"fmt"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/internal/cri/store/container"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToCRIContainerUser(t *testing.T) {
	fakeErrorOnSpec := errors.New("error")
	testCases := []struct {
		name        string
		container   containerd.Container
		expected    *runtime.ContainerUser
		expectErr   bool
		expectedErr error
	}{
		{
			name:        "container is nil",
			container:   nil,
			expectErr:   true,
			expectedErr: errors.New("container must not be nil"),
		},
		{
			name: "Spec() returns error",
			container: &fakeSpecOnlyContainer{
				t:         t,
				errOnSpec: fakeErrorOnSpec,
			},
			expectErr:   true,
			expectedErr: fmt.Errorf("failed to get container runtime spec: %w", fakeErrorOnSpec),
		},
		{
			name: "no Process",
			container: &fakeSpecOnlyContainer{
				t:    t,
				spec: &specs.Spec{},
			},
			expected: &runtime.ContainerUser{},
		},
		{
			name: "no additionalGids",
			container: &fakeSpecOnlyContainer{
				t: t,
				spec: &specs.Spec{
					Process: &specs.Process{
						User: specs.User{
							UID: 0,
							GID: 0,
						},
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
			container: &fakeSpecOnlyContainer{
				t: t,
				spec: &specs.Spec{
					Process: &specs.Process{
						User: specs.User{
							UID:            0,
							GID:            0,
							AdditionalGids: []uint32{0, 1234},
						},
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
				Container: testCase.container,
			})
			if testCase.expectErr {
				require.Nil(t, got)
				require.Error(t, err)
				assert.Equal(t, testCase.expectedErr, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.expected, got)
			}
		})
	}
}
