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
	"fmt"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// LayerIDs resolves the per-layer identifier for each of a manifest's
// layers. A layer ID is a cryptographic digest which uniquely and securely
// identifies the layer's applied content: traditionally an OCI DiffID (the
// digest of the uncompressed tar), but equally the digest of an
// uncompressed filesystem image, a chunk index, or the layer blob itself,
// depending on the layer's media type. ChainIDs derived from these IDs are
// used as content-derived snapshot keys, so the ID returned for a layer
// MUST always be one that is verified before any snapshot is keyed on it:
//
//   - The blob digest is verified by the content store on ingest (a
//     content.Writer's Commit rejects a digest mismatch).
//   - Any other ID is verified by the unpacker, which compares it against
//     the digest the applier computes over the applied content.
//
// A layer which is never applied (see IsSkippableLayerType) has no applier
// to verify it, so it is always identified by its blob digest - the only
// value verified independently of an applier. If such a layer also
// declares an org.erofs.uncompressed-digest annotation or a rootfs.diff_ids
// entry, that declared value MUST agree with the blob digest: since it can
// never be verified by an applier, a disagreement would mean the ChainID
// computed here differs from the one the image's producer intended, which
// is treated as an error rather than silently accepted or overridden.
//
// Per the EROFS image layer format specification
// (https://github.com/erofs/erofs-image-spec, §5.2), a layer's ID is
// resolved as follows:
//
//   - If a layer descriptor carries the AnnotationErofsUncompressedDigest
//     annotation, that value is used, subject to the skippable-layer
//     agreement rule above.
//   - Otherwise, the corresponding entry in diffIDs (usually sourced from
//     the image configuration's rootfs.diff_ids) is used, when present,
//     again subject to that rule.
//   - Otherwise, the layer's own descriptor (blob) digest is used. This
//     covers layers that are inherently self-addressing under the
//     specification (a raw, uncompressed EROFS blob, or a standalone
//     chunk-index layer), as well as any other layer for which neither an
//     annotation nor a diffIDs entry is present - such a layer is not
//     rejected outright, since its applier will independently verify the
//     blob digest against the applied content and fail loudly on a
//     mismatch.
//
// The last layer (and, transitively, every layer if all are skippable)
// MUST NOT be skippable: per the specification (§3.8 rule 1) the last
// entry in a manifest's layers MUST be mountable, and a skipped last layer
// would leave the final ChainID - the one used to look up the unpacked
// rootfs - with no corresponding snapshot.
//
// For manifests that use none of the above EROFS extensions, diffIDs must
// have exactly one entry per layer and is returned unchanged in order,
// preserving the traditional OCI behavior exactly.
func LayerIDs(layers []ocispec.Descriptor, diffIDs []digest.Digest) ([]digest.Digest, error) {
	if len(diffIDs) > len(layers) {
		return nil, fmt.Errorf("number of layers and diffIDs don't match: %d != %d", len(layers), len(diffIDs))
	}
	if len(layers) > 0 && IsSkippableLayerType(layers[len(layers)-1].MediaType) {
		return nil, fmt.Errorf("last layer (%s %s) must not be skippable: the final chain id would have no corresponding snapshot", layers[len(layers)-1].MediaType, layers[len(layers)-1].Digest)
	}

	result := make([]digest.Digest, len(layers))
	for i, l := range layers {
		var (
			declared    digest.Digest
			hasDeclared bool
		)
		switch {
		case l.Annotations[AnnotationErofsUncompressedDigest] != "":
			d, err := digest.Parse(l.Annotations[AnnotationErofsUncompressedDigest])
			if err != nil {
				return nil, fmt.Errorf("invalid %s annotation on layer %d (%s): %w", AnnotationErofsUncompressedDigest, i, l.Digest, err)
			}
			declared, hasDeclared = d, true
		case i < len(diffIDs):
			declared, hasDeclared = diffIDs[i], true
		}

		if IsSkippableLayerType(l.MediaType) {
			// Never applied, so never verified by an applier: the blob
			// digest - verified by the content store on ingest - is the
			// only value this layer can be safely identified by.
			if hasDeclared && declared != l.Digest {
				return nil, fmt.Errorf("layer %d (%s %s) declares id %s, which disagrees with its own (blob) digest; this layer is never applied, so the declared value can never be verified", i, l.MediaType, l.Digest, declared)
			}
			result[i] = l.Digest
			continue
		}

		if hasDeclared {
			result[i] = declared
		} else {
			result[i] = l.Digest
		}
	}
	return result, nil
}
