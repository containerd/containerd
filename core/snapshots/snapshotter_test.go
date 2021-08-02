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

package snapshots

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithQuotaSize(t *testing.T) {
	testCases := []struct {
		name        string
		size        string
		expectedErr string
		expected    map[string]string
	}{
		{
			name:     "EmptySize",
			size:     "",
			expected: map[string]string{},
		},
		{
			name: "ValidSize",
			size: "1GB",
			expected: map[string]string{
				LabelSnapshotQuotaSize: "1GB",
			},
		},
		{
			name:        "InvalidSize",
			size:        "invalid",
			expectedErr: "invalid size: 'invalid'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			info := &Info{
				Labels: make(map[string]string),
			}
			err := WithQuotaSize(tc.size)(info)
			if tc.expectedErr != "" {
				require.EqualError(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expected, info.Labels)
			}
		})
	}
}
