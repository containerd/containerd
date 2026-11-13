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

package fsview

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/stretchr/testify/require"
)

func TestResolveOverlayValue(t *testing.T) {
	dir := t.TempDir()

	// Create two mount sources with distinguishable contents.
	sources := make([]string, 2)
	for i, name := range []string{"first", "second"} {
		source := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Join(source, "subdir"), 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(source, "marker"),
			[]byte(name),
			0o644,
		))
		require.NoError(t, os.WriteFile(
			filepath.Join(source, "subdir", "marker"),
			[]byte(name+"-subdir"),
			0o644,
		))
		sources[i] = source
	}

	mounts := []mount.Mount{
		{Type: "bind", Source: sources[0]},
		{Type: "bind", Source: sources[1]},
	}

	tests := []struct {
		name    string
		value   string
		want    []string
		wantErr bool
	}{
		{
			name:  "plain path",
			value: sources[0],
			want:  []string{"first"},
		},
		{
			name:  "source",
			value: "{{ source 0 }}",
			want:  []string{"first"},
		},
		{
			name:  "mount",
			value: "{{ mount 1 }}",
			want:  []string{"second"},
		},
		{
			name:  "overlay ascending",
			value: "{{ overlay 0 1 }}",
			want:  []string{"first", "second"},
		},
		{
			name:  "overlay descending",
			value: "{{ overlay 1 0 }}",
			want:  []string{"second", "first"},
		},
		{
			name:  "multiple actions",
			value: "{{ mount 0 }}{{ mount 1 }}",
			want:  []string{"first", "second"},
		},
		{
			name:  "whitespace trimming",
			value: "{{- mount 0 -}}{{ mount 1 }}",
			want:  []string{"first", "second"},
		},
		{
			name:  "text between actions",
			value: "{{ mount 0 }}ignored{{ mount 1 }}",
			want:  []string{"first", "second"},
		},
		{
			name:  "path suffix",
			value: "{{ mount 0 }}/subdir",
			want:  []string{"first-subdir"},
		},
		{
			name:  "suffix with multiple actions",
			value: "{{ mount 0 }}{{ mount 1 }}/subdir",
			want:  []string{"first-subdir", "second-subdir"},
		},
		{
			name:    "negative index",
			value:   "{{ mount -1 }}",
			wantErr: true,
		},
		{
			name:    "out of bounds",
			value:   "{{ mount 2 }}",
			wantErr: true,
		},
		{
			name:    "invalid argument",
			value:   `{{ mount "0" }}`,
			wantErr: true,
		},
		{
			name:    "unknown function",
			value:   "{{ unknown 0 }}",
			wantErr: true,
		},
		{
			name:    "invalid syntax",
			value:   "{{ mount 0",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			views, err := resolveOverlayValue(tt.value, mounts)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, views, len(tt.want))

			for i, view := range views {
				t.Cleanup(func() {
					require.NoError(t, view.Close())
				})

				data, err := fs.ReadFile(view, "marker")
				require.NoError(t, err)
				require.Equal(t, tt.want[i], string(data))
			}
		})
	}
}
