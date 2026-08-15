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

package images

import (
	"testing"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLayerIDsClassicImage(t *testing.T) {
	d0 := digest.FromString("layer0")
	d1 := digest.FromString("layer1")
	layers := []ocispec.Descriptor{
		{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString("blob0")},
		{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString("blob1")},
	}

	got, err := LayerIDs(layers, []digest.Digest{d0, d1})
	require.NoError(t, err)
	assert.Equal(t, []digest.Digest{d0, d1}, got)
}

// TestLayerIDsMissingDiffIDsFallsBackToBlobDigest verifies the permissive
// default: a layer with neither an annotation nor a rootfs.diff_ids entry
// is not rejected outright. It resolves to its own blob digest, which its
// applier will independently verify against the applied content, so an
// image that actually disagrees still fails loudly - just later, at unpack
// time, rather than here.
func TestLayerIDsMissingDiffIDsFallsBackToBlobDigest(t *testing.T) {
	blobDigest := digest.FromString("blob0")
	layers := []ocispec.Descriptor{
		{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: blobDigest},
	}

	got, err := LayerIDs(layers, nil)
	require.NoError(t, err)
	assert.Equal(t, []digest.Digest{blobDigest}, got)
}

func TestLayerIDsTooManyDiffIDsErrors(t *testing.T) {
	layers := []ocispec.Descriptor{
		{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString("blob0")},
	}
	_, err := LayerIDs(layers, []digest.Digest{digest.FromString("a"), digest.FromString("b")})
	require.Error(t, err)
}

func TestLayerIDsAnnotationTakesPrecedence(t *testing.T) {
	uncompressed := digest.FromString("uncompressed-content")
	wrongDiffID := digest.FromString("stale-diff-id-from-config")
	layers := []ocispec.Descriptor{
		{
			MediaType:   MediaTypeErofs + "+zstd",
			Digest:      digest.FromString("compressed-blob"),
			Annotations: map[string]string{AnnotationErofsUncompressedDigest: uncompressed.String()},
		},
	}

	got, err := LayerIDs(layers, []digest.Digest{wrongDiffID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, uncompressed, got[0])
}

func TestLayerIDsAnnotationOnlyImage(t *testing.T) {
	uncompressed0 := digest.FromString("uncompressed-0")
	uncompressed1 := digest.FromString("uncompressed-1")
	layers := []ocispec.Descriptor{
		{
			MediaType:   MediaTypeErofs + "+zstd",
			Digest:      digest.FromString("compressed-blob-0"),
			Annotations: map[string]string{AnnotationErofsUncompressedDigest: uncompressed0.String()},
		},
		{
			MediaType:   MediaTypeErofs + "+zstd",
			Digest:      digest.FromString("compressed-blob-1"),
			Annotations: map[string]string{AnnotationErofsUncompressedDigest: uncompressed1.String()},
		},
	}

	// rootfs.diff_ids entirely absent, as allowed by the specification when
	// every compressed layer carries the annotation.
	got, err := LayerIDs(layers, nil)
	require.NoError(t, err)
	assert.Equal(t, []digest.Digest{uncompressed0, uncompressed1}, got)
}

func TestLayerIDsInvalidAnnotationErrors(t *testing.T) {
	layers := []ocispec.Descriptor{
		{
			MediaType:   MediaTypeErofs + "+zstd",
			Digest:      digest.FromString("compressed-blob"),
			Annotations: map[string]string{AnnotationErofsUncompressedDigest: "not-a-digest"},
		},
	}
	_, err := LayerIDs(layers, nil)
	require.Error(t, err)
}

func TestLayerIDsRawErofsSelfDigest(t *testing.T) {
	rawDigest := digest.FromString("raw-erofs-blob")
	layers := []ocispec.Descriptor{
		{MediaType: MediaTypeErofs, Digest: rawDigest},
	}

	// No annotation (redundant for raw layers per the spec) and no
	// rootfs.diff_ids: ID falls back to the descriptor (blob) digest.
	got, err := LayerIDs(layers, nil)
	require.NoError(t, err)
	assert.Equal(t, []digest.Digest{rawDigest}, got)

	// Legacy media type behaves the same way.
	layers[0].MediaType = MediaTypeErofsLayer
	got, err = LayerIDs(layers, nil)
	require.NoError(t, err)
	assert.Equal(t, []digest.Digest{rawDigest}, got)
}

func TestLayerIDsStandaloneChunkIndexSelfDigest(t *testing.T) {
	chunkIndexDigest := digest.FromString("chunk-index-blob")
	mainLayerID := digest.FromString("main-layer-id")
	// The chunk-index layer must not be last (§3.8 rule 1), so it is
	// followed by a third, mountable layer here.
	layers := []ocispec.Descriptor{
		{MediaType: MediaTypeErofs + "+zstd", Digest: digest.FromString("compressed-blob")},
		{MediaType: MediaTypeErofsChunkIndex, Digest: chunkIndexDigest},
		{MediaType: MediaTypeErofs, Digest: digest.FromString("top-blob")},
	}

	// diff_ids only covers the first (compressed) layer; the standalone
	// chunk-index layer has no annotation and no diffIDs entry, so it must
	// fall back to its own descriptor digest, and the third layer falls
	// back to its own (raw, self-addressing) descriptor digest too.
	got, err := LayerIDs(layers, []digest.Digest{mainLayerID})
	require.NoError(t, err)
	assert.Equal(t, []digest.Digest{mainLayerID, chunkIndexDigest, layers[2].Digest}, got)
}

// TestLayerIDsCompressedLayerWithoutSourceFallsBackToBlobDigest verifies
// that a compressed EROFS layer with neither the annotation nor a
// rootfs.diff_ids entry falls back to its own (compressed) blob digest,
// like any other layer type. Since the layer's ID is not actually its
// uncompressed content's digest in this case, the differ's own digest
// comparison at apply time will catch the mismatch and fail loudly.
func TestLayerIDsCompressedLayerWithoutSourceFallsBackToBlobDigest(t *testing.T) {
	blobDigest := digest.FromString("compressed-blob")
	layers := []ocispec.Descriptor{
		{MediaType: MediaTypeErofs + "+zstd", Digest: blobDigest},
	}
	got, err := LayerIDs(layers, nil)
	require.NoError(t, err)
	assert.Equal(t, []digest.Digest{blobDigest}, got)
}

func TestLayerIDsTrailingSkippableLayerErrors(t *testing.T) {
	layers := []ocispec.Descriptor{
		{MediaType: MediaTypeErofs, Digest: digest.FromString("bottom-blob")},
		{MediaType: MediaTypeErofsChunkIndex, Digest: digest.FromString("chunk-index-blob")},
	}
	_, err := LayerIDs(layers, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be skippable")
}

func TestLayerIDsSingleSkippableLayerErrors(t *testing.T) {
	// A single-layer manifest whose only layer is skippable is, trivially,
	// a trailing skippable layer.
	layers := []ocispec.Descriptor{
		{MediaType: MediaTypeErofsChunkIndex, Digest: digest.FromString("chunk-index-blob")},
	}
	_, err := LayerIDs(layers, nil)
	require.Error(t, err)
}

// TestLayerIDsSkippableLayerAgreeingDeclaredValue verifies that a
// skippable layer whose declared annotation/diff_ids value agrees with its
// own blob digest is accepted (the common, spec-conformant case: the
// blob digest *is* the chunk index's defined DiffID).
func TestLayerIDsSkippableLayerAgreeingDeclaredValue(t *testing.T) {
	chunkIndexDigest := digest.FromString("chunk-index-blob")
	layers := []ocispec.Descriptor{
		{
			MediaType:   MediaTypeErofsChunkIndex,
			Digest:      chunkIndexDigest,
			Annotations: map[string]string{AnnotationErofsUncompressedDigest: chunkIndexDigest.String()},
		},
		{MediaType: MediaTypeErofs, Digest: digest.FromString("top-blob")},
	}
	got, err := LayerIDs(layers, nil)
	require.NoError(t, err)
	assert.Equal(t, chunkIndexDigest, got[0])
}

// TestLayerIDsSkippableLayerDisagreeingAnnotationErrors verifies that a
// skippable layer's org.erofs.uncompressed-digest annotation, if present,
// MUST agree with its own blob digest: since the layer is never applied,
// nothing else can verify the declared value, and using it anyway would
// mean computing a ChainID the image's producer did not intend.
func TestLayerIDsSkippableLayerDisagreeingAnnotationErrors(t *testing.T) {
	layers := []ocispec.Descriptor{
		{
			MediaType:   MediaTypeErofsChunkIndex,
			Digest:      digest.FromString("chunk-index-blob"),
			Annotations: map[string]string{AnnotationErofsUncompressedDigest: digest.FromString("attacker-chosen").String()},
		},
		{MediaType: MediaTypeErofs, Digest: digest.FromString("top-blob")},
	}
	_, err := LayerIDs(layers, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disagrees")
}

// TestLayerIDsSkippableLayerDisagreeingDiffIDErrors is the same as above,
// but for a disagreeing rootfs.diff_ids entry instead of the annotation.
func TestLayerIDsSkippableLayerDisagreeingDiffIDErrors(t *testing.T) {
	layers := []ocispec.Descriptor{
		{MediaType: MediaTypeErofsChunkIndex, Digest: digest.FromString("chunk-index-blob")},
		{MediaType: MediaTypeErofs, Digest: digest.FromString("top-blob")},
	}
	diffIDs := []digest.Digest{digest.FromString("attacker-chosen"), digest.FromString("top-blob")}
	_, err := LayerIDs(layers, diffIDs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disagrees")
}
