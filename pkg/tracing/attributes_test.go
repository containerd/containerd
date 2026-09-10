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

package tracing

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestLegacyAttributes(t *testing.T) {
	tests := []struct {
		name     string
		input    []attribute.KeyValue
		expected []attribute.KeyValue
	}{
		{
			name:     "ContainerID",
			input:    []attribute.KeyValue{attribute.String("container.id", "container-1")},
			expected: []attribute.KeyValue{attribute.String("task.container.id", "container-1")},
		},
		{
			name:     "ContainerRuntimeName",
			input:    []attribute.KeyValue{attribute.String("container.runtime.name", "io.containerd.runc.v2")},
			expected: []attribute.KeyValue{attribute.String("task.runtime.name", "io.containerd.runc.v2")},
		},
		{
			name:  "ProcessPIDExpandsToBothLegacyKeys",
			input: []attribute.KeyValue{attribute.Int("process.pid", 1234)},
			expected: []attribute.KeyValue{
				attribute.Int("task.pid", 1234),
				attribute.Int("task.process.id", 1234),
			},
		},
		{
			name:     "ContainerImageID",
			input:    []attribute.KeyValue{attribute.String("container.image.id", "sha256:19c92d0a00d1b66d897bceaa7319bee0dd38a10a851c60bcec9474aa3f01e50f")},
			expected: []attribute.KeyValue{attribute.String("image.id", "sha256:19c92d0a00d1b66d897bceaa7319bee0dd38a10a851c60bcec9474aa3f01e50f")},
		},
		{
			name:     "ImageRef",
			input:    []attribute.KeyValue{attribute.String("containerd.image.ref", "docker.io/library/busybox:latest")},
			expected: []attribute.KeyValue{attribute.String("image.ref", "docker.io/library/busybox:latest")},
		},
		{
			name:  "SnapshotterNameExpandsToBothLegacyKeys",
			input: []attribute.KeyValue{attribute.String("containerd.snapshotter.name", "overlayfs")},
			expected: []attribute.KeyValue{
				attribute.String("snapshotter.name", "overlayfs"),
				attribute.String("container.snapshotter.name", "overlayfs"),
			},
		},
		{
			name:     "Unpack",
			input:    []attribute.KeyValue{attribute.Bool("containerd.pull.unpack", true)},
			expected: []attribute.KeyValue{attribute.Bool("unpack", true)},
		},
		{
			name: "UnknownKeysIgnored",
			input: []attribute.KeyValue{
				attribute.Int("max.concurrent.downloads", 3),
				attribute.String("container.image.ref", "docker.io/library/redis:latest"),
			},
			expected: nil,
		},
		{
			name: "MixedKnownAndUnknownPreservesInputOrder",
			input: []attribute.KeyValue{
				attribute.String("containerd.image.ref", "docker.io/library/busybox:latest"),
				attribute.Bool("containerd.pull.unpack", false),
				attribute.Int("platforms.count", 1),
			},
			expected: []attribute.KeyValue{
				attribute.String("image.ref", "docker.io/library/busybox:latest"),
				attribute.Bool("unpack", false),
			},
		},
		{
			name:     "EmptyInput",
			input:    nil,
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, LegacyAttributes(tc.input))
		})
	}
}
