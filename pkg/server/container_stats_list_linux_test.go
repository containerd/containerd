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

	v1 "github.com/containerd/cgroups/stats/v1"
	"github.com/stretchr/testify/assert"
)

func TestGetWorkingSet(t *testing.T) {
	for desc, test := range map[string]struct {
		memory   *v1.MemoryStat
		expected uint64
	}{
		"nil memory usage": {
			memory:   &v1.MemoryStat{},
			expected: 0,
		},
		"memory usage higher than inactive_total_file": {
			memory: &v1.MemoryStat{
				TotalInactiveFile: 1000,
				Usage:             &v1.MemoryEntry{Usage: 2000},
			},
			expected: 1000,
		},
		"memory usage lower than inactive_total_file": {
			memory: &v1.MemoryStat{
				TotalInactiveFile: 2000,
				Usage:             &v1.MemoryEntry{Usage: 1000},
			},
			expected: 0,
		},
	} {
		t.Run(desc, func(t *testing.T) {
			got := getWorkingSet(test.memory)
			assert.Equal(t, test.expected, got)
		})
	}
}
