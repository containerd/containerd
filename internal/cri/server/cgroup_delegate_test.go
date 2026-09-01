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

	"github.com/stretchr/testify/require"
)

func TestCgroupDelegateAnnotations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		cgroupWritable  bool
		unifiedCgroups  bool
		privileged      bool
		wantAnnotations map[string]string
	}{
		{
			name:            "writable cgroup v2 unprivileged",
			cgroupWritable:  true,
			unifiedCgroups:  true,
			privileged:      false,
			wantAnnotations: map[string]string{"org.systemd.property.Delegate": "true"},
		},
		{
			name:           "writable cgroup v2 privileged",
			cgroupWritable: true,
			unifiedCgroups: true,
			privileged:     true,
		},
		{
			name:           "writable cgroup v1",
			cgroupWritable: true,
			unifiedCgroups: false,
			privileged:     false,
		},
		{
			name:           "read-only cgroup v2",
			cgroupWritable: false,
			unifiedCgroups: true,
			privileged:     false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.wantAnnotations, cgroupDelegateAnnotations(tc.cgroupWritable, tc.unifiedCgroups, tc.privileged))
		})
	}
}
