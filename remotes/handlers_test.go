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

package remotes

import (
	"context"
	"testing"

	"github.com/containerd/containerd/images"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestContextCustomKeyPrefix(t *testing.T) {
	ctx := context.Background()
	cmt := "testing/custom.media.type"
	ctx = WithMediaTypeKeyPrefix(ctx, images.MediaTypeDockerSchema2Layer, "bananas")
	ctx = WithMediaTypeKeyPrefix(ctx, cmt, "apples")

	// makes sure that even though we've supplied some custom handling, the built-in still works
	t.Run("normal supported case", func(t *testing.T) {
		desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayer}
		expected := "layer-"

		actual := MakeRefKey(ctx, desc)
		if actual != expected {
			t.Fatalf("unexpected ref key, expected %s, got: %s", expected, actual)
		}
	})

	t.Run("unknown media type", func(t *testing.T) {
		desc := ocispec.Descriptor{MediaType: "we.dont.know.what.this.is"}
		expected := "unknown-"

		actual := MakeRefKey(ctx, desc)
		if actual != expected {
			t.Fatalf("unexpected ref key, expected %s, got: %s", expected, actual)
		}
	})

	t.Run("overwrite supported media type", func(t *testing.T) {
		desc := ocispec.Descriptor{MediaType: images.MediaTypeDockerSchema2Layer}
		expected := "bananas-"

		actual := MakeRefKey(ctx, desc)
		if actual != expected {
			t.Fatalf("unexpected ref key, expected %s, got: %s", expected, actual)
		}
	})

	t.Run("custom media type", func(t *testing.T) {
		desc := ocispec.Descriptor{MediaType: cmt}
		expected := "apples-"

		actual := MakeRefKey(ctx, desc)
		if actual != expected {
			t.Fatalf("unexpected ref key, expected %s, got: %s", expected, actual)
		}
	})
}
