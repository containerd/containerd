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

func TestMergeExecEnv(t *testing.T) {
	tests := []struct {
		name    string
		base    []string
		overlay []string
		want    []string
	}{
		{
			name:    "nil overlay returns base unchanged",
			base:    []string{"A=1", "B=2"},
			overlay: nil,
			want:    []string{"A=1", "B=2"},
		},
		{
			name:    "empty overlay returns base unchanged",
			base:    []string{"A=1"},
			overlay: []string{},
			want:    []string{"A=1"},
		},
		{
			name:    "override existing key",
			base:    []string{"A=1", "B=2"},
			overlay: []string{"B=3"},
			want:    []string{"A=1", "B=3"},
		},
		{
			name:    "append new key",
			base:    []string{"A=1"},
			overlay: []string{"C=3"},
			want:    []string{"A=1", "C=3"},
		},
		{
			name:    "override and append",
			base:    []string{"A=1", "B=2"},
			overlay: []string{"B=x", "C=y"},
			want:    []string{"A=1", "B=x", "C=y"},
		},
		{
			name:    "duplicate overlay keys last wins",
			base:    []string{"A=1"},
			overlay: []string{"B=first", "B=second"},
			want:    []string{"A=1", "B=second"},
		},
		{
			name:    "value containing equals sign",
			base:    []string{"A=b=c"},
			overlay: []string{"A=d=e=f"},
			want:    []string{"A=d=e=f"},
		},
		{
			name:    "empty overlay value",
			base:    []string{"A=1"},
			overlay: []string{"B="},
			want:    []string{"A=1", "B="},
		},
		{
			name:    "case sensitive keys",
			base:    []string{"a=1", "A=2"},
			overlay: []string{"A=upper"},
			want:    []string{"a=1", "A=upper"},
		},
		{
			name:    "audit ID injection scenario",
			base:    []string{"PATH=/usr/bin", "HOME=/root"},
			overlay: []string{"KUBERNETES_EXEC_AUDIT_ID=f4a3b2c1-1234-5678-9abc-def012345678"},
			want:    []string{"PATH=/usr/bin", "HOME=/root", "KUBERNETES_EXEC_AUDIT_ID=f4a3b2c1-1234-5678-9abc-def012345678"},
		},
		{
			name:    "no variable expansion",
			base:    []string{"FOO=bar"},
			overlay: []string{"BAZ=$FOO"},
			want:    []string{"FOO=bar", "BAZ=$FOO"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeExecEnv(tc.base, tc.overlay)
			assert.Equal(t, tc.want, got)
		})
	}
}
