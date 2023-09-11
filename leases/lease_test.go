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

package leases

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithLabels(t *testing.T) {
	testcases := []struct {
		name          string
		initialLabels map[string]string
		labels        map[string]string
		expected      map[string]string
	}{
		{
			name: "AddLabelsToEmptyMap",
			labels: map[string]string{
				"containerd.io/gc.root": "2015-12-04T00:00:00Z",
			},
			expected: map[string]string{
				"containerd.io/gc.root": "2015-12-04T00:00:00Z",
			},
		},
		{
			name: "AddLabelsToNonEmptyMap",
			initialLabels: map[string]string{
				"containerd.io/gc.expire": "2015-12-05T00:00:00Z",
			},
			labels: map[string]string{
				"containerd.io/gc.root":                   "2015-12-04T00:00:00Z",
				"containerd.io/gc.ref.snapshot.overlayfs": "sha256:87806a591ce894ff5c699c28fe02093d6cdadd6b1ad86819acea05ccb212ff3d",
			},
			expected: map[string]string{
				"containerd.io/gc.root":                   "2015-12-04T00:00:00Z",
				"containerd.io/gc.ref.snapshot.overlayfs": "sha256:87806a591ce894ff5c699c28fe02093d6cdadd6b1ad86819acea05ccb212ff3d",
				"containerd.io/gc.expire":                 "2015-12-05T00:00:00Z",
			},
		},
	}

	for _, tc := range testcases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			lease := newLease(tc.initialLabels)
			err := WithLabels(tc.labels)(lease)
			require.NoError(t, err)
			assert.Equal(t, lease.Labels, tc.expected)
		})
	}
	for _, tc := range testcases {
		tc := tc
		t.Run(tc.name+"-WithLabel", func(t *testing.T) {
			lease := newLease(tc.initialLabels)
			for k, v := range tc.labels {
				err := WithLabel(k, v)(lease)
				require.NoError(t, err)
			}
			assert.Equal(t, lease.Labels, tc.expected)
		})
	}
}

func newLease(labels map[string]string) *Lease {
	lease := &Lease{}
	if labels != nil {
		lease.Labels = map[string]string{}
		for k, v := range labels {
			lease.Labels[k] = v
		}
	}
	return lease
}
