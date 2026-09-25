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
	"go.opentelemetry.io/otel/attribute"
)

// legacyAttributes maps current attribute keys to the deprecated legacy keys
// that are still emitted during the migration period.
var legacyAttributes = map[attribute.Key][]attribute.Key{
	"container.id":                {"task.container.id"},
	"container.runtime.name":      {"task.runtime.name"},
	"process.pid":                 {"task.pid", "task.process.id"},
	"container.image.id":          {"image.id"},
	"containerd.image.ref":        {"image.ref"},
	"containerd.snapshotter.name": {"snapshotter.name", "container.snapshotter.name"},
	"containerd.pull.unpack":      {"unpack"},
}

// LegacyAttributes returns the deprecated legacy attributes corresponding to
// attrs. The legacy keys will be removed in a future release.
func LegacyAttributes(attrs []attribute.KeyValue) []attribute.KeyValue {
	var legacy []attribute.KeyValue
	for _, attr := range attrs {
		for _, key := range legacyAttributes[attr.Key] {
			legacy = append(legacy, attribute.KeyValue{Key: key, Value: attr.Value})
		}
	}
	return legacy
}
