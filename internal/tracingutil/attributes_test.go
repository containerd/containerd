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
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestContextWithAttributes(t *testing.T) {
	ctx := context.Background()
	assert.Empty(t, AttributesFromContext(ctx))

	ctx = ContextWithAttributes(ctx, attribute.String("a", "1"), attribute.String("b", "1"))
	assert.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("a", "1"),
		attribute.String("b", "1"),
	}, AttributesFromContext(ctx))

	// New attributes are merged, replacing the existing ones with the same key.
	child := ContextWithAttributes(ctx, attribute.String("b", "2"), attribute.String("c", "2"))
	assert.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("a", "1"),
		attribute.String("b", "2"),
		attribute.String("c", "2"),
	}, AttributesFromContext(child))

	// The parent context is not affected.
	assert.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("a", "1"),
		attribute.String("b", "1"),
	}, AttributesFromContext(ctx))
}
