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

package tracingutil

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
)

type attributesKey struct{}

// ContextWithAttributes returns the context including span attributes.
// If there are duplicate keys, the new attributes will replace the existing ones.
func ContextWithAttributes(ctx context.Context, kv ...attribute.KeyValue) context.Context {
	set := attribute.NewSet(append(AttributesFromContext(ctx), kv...)...)
	return context.WithValue(ctx, attributesKey{}, set)
}

// AttributesFromContext returns the span attributes from the context.
func AttributesFromContext(ctx context.Context) []attribute.KeyValue {
	set, _ := ctx.Value(attributesKey{}).(attribute.Set)
	return set.ToSlice()
}
