// +build !windows

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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetCgroupsPath(t *testing.T) {
	testID := "test-id"
	for desc, test := range map[string]struct {
		cgroupsParent string
		expected      string
	}{
		"should support regular cgroup path": {
			cgroupsParent: "/a/b",
			expected:      "/a/b/test-id",
		},
		"should support systemd cgroup path": {
			cgroupsParent: "/a.slice/b.slice",
			expected:      "b.slice:cri-containerd:test-id",
		},
		"should support tailing slash for regular cgroup path": {
			cgroupsParent: "/a/b/",
			expected:      "/a/b/test-id",
		},
		"should support tailing slash for systemd cgroup path": {
			cgroupsParent: "/a.slice/b.slice/",
			expected:      "b.slice:cri-containerd:test-id",
		},
		"should treat root cgroup as regular cgroup path": {
			cgroupsParent: "/",
			expected:      "/test-id",
		},
	} {
		t.Logf("TestCase %q", desc)
		got := getCgroupsPath(test.cgroupsParent, testID)
		assert.Equal(t, test.expected, got)
	}
}
