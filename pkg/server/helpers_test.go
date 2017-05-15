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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
)

func TestGetSandbox(t *testing.T) {
	c := newTestCRIContainerdService()
	testID := "abcdefg"
	testSandbox := metadata.SandboxMetadata{
		ID:   testID,
		Name: "test-name",
	}
	assert.NoError(t, c.sandboxStore.Create(testSandbox))
	assert.NoError(t, c.sandboxIDIndex.Add(testID))

	for desc, test := range map[string]struct {
		id        string
		expected  *metadata.SandboxMetadata
		expectErr bool
	}{
		"full id": {
			id:        testID,
			expected:  &testSandbox,
			expectErr: false,
		},
		"partial id": {
			id:        testID[:3],
			expected:  &testSandbox,
			expectErr: false,
		},
		"non-exist id": {
			id:        "gfedcba",
			expected:  nil,
			expectErr: true,
		},
	} {
		t.Logf("TestCase %q", desc)
		sb, err := c.getSandbox(test.id)
		if test.expectErr {
			assert.Error(t, err)
			assert.True(t, metadata.IsNotExistError(err))
		} else {
			assert.NoError(t, err)
		}
		assert.Equal(t, test.expected, sb)
	}
}
