//go:build linux

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

package oom

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadMemoryOOMKill(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    uint64
	}{
		{
			name:    "no oom kills",
			content: "low 0\nhigh 0\nmax 0\noom 0\noom_kill 0\noom_group_kill 0\n",
			want:    0,
		},
		{
			name:    "with oom kills",
			content: "low 12\nhigh 34\nmax 56\noom 7\noom_kill 3\noom_group_kill 1\n",
			want:    3,
		},
		{
			// oom_group_kill must not be mistaken for oom_kill, and the high
			// counter (which churns most frequently) must be ignored.
			name:    "oom_group_kill only",
			content: "low 0\nhigh 99999\nmax 1234\noom 0\noom_kill 0\noom_group_kill 5\n",
			want:    0,
		},
		{
			name:    "missing oom_kill line",
			content: "low 0\nhigh 0\nmax 0\n",
			want:    0,
		},
		{
			name:    "no trailing newline",
			content: "low 0\nhigh 0\nmax 0\noom 0\noom_kill 42\noom_group_kill 0",
			want:    42,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "memory.events"), []byte(tc.content), 0o644))

			buf := make([]byte, memoryEventsBufSize)
			got, err := readMemoryOOMKill(dir, buf)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestReadMemoryOOMKillMissingFile(t *testing.T) {
	buf := make([]byte, memoryEventsBufSize)
	_, err := readMemoryOOMKill(t.TempDir(), buf)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
