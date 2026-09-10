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
	"context"
	"testing"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeChildrenHandler(children map[digest.Digest][]ocispec.Descriptor) HandlerFunc {
	return func(_ context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		return children[desc.Digest], nil
	}
}

func plainAndErofsManifestDescs() (plain, erofs ocispec.Descriptor) {
	plain = ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromString("manifest-plain"),
		Platform:  &ocispec.Platform{OS: "linux", Architecture: "amd64"},
	}
	erofs = ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromString("manifest-erofs"),
		Platform:  &ocispec.Platform{OS: "linux", Architecture: "amd64", OSFeatures: []string{"erofs"}},
	}
	return
}

func digestsOf(descs []ocispec.Descriptor) []digest.Digest {
	digests := make([]digest.Digest, len(descs))
	for i, d := range descs {
		digests[i] = d.Digest
	}
	return digests
}

var (
	amd64Spec      = ocispec.Platform{OS: "linux", Architecture: "amd64"}
	amd64ErofsSpec = ocispec.Platform{OS: "linux", Architecture: "amd64", OSFeatures: []string{"erofs"}}
	arm64Spec      = ocispec.Platform{OS: "linux", Architecture: "arm64"}
	i386Spec       = ocispec.Platform{OS: "linux", Architecture: "386"}
)

func TestSelectManifestsByPlatformSingleManifestPerPlatformIsANoOp(t *testing.T) {
	amd64 := ocispec.Descriptor{Digest: digest.FromString("amd64"), Platform: &amd64Spec}
	arm64 := ocispec.Descriptor{Digest: digest.FromString("arm64"), Platform: &arm64Spec}
	children := []ocispec.Descriptor{amd64, arm64}

	selected := selectManifestsByPlatform(children, []ocispec.Platform{amd64Spec, arm64Spec})
	assert.Equal(t, 2, selected)
	assert.Equal(t, []ocispec.Descriptor{amd64, arm64}, children, "order should be unchanged when nothing needs to move")
}

func TestSelectManifestsByPlatformPrefersMostOSFeatures(t *testing.T) {
	plain, erofs := plainAndErofsManifestDescs()
	children := []ocispec.Descriptor{plain, erofs}

	selected := selectManifestsByPlatform(children, []ocispec.Platform{amd64ErofsSpec})
	require.Equal(t, 1, selected, "only one manifest is selected for the single requested platform")
	assert.Equal(t, erofs.Digest, children[0].Digest, "the richer (erofs) variant is preferred")
}

func TestSelectManifestsByPlatformIgnoresNonMatching(t *testing.T) {
	plain, erofs := plainAndErofsManifestDescs()
	other := ocispec.Descriptor{Digest: digest.FromString("arm64"), Platform: &arm64Spec}
	children := []ocispec.Descriptor{plain, erofs, other}

	selected := selectManifestsByPlatform(children, []ocispec.Platform{amd64ErofsSpec})
	require.Equal(t, 1, selected)
	assert.Equal(t, erofs.Digest, children[0].Digest)
	assert.ElementsMatch(t, []digest.Digest{plain.Digest, other.Digest}, digestsOf(children[selected:]),
		"non-selected children remain, unmatched or not, after the selected prefix")
}

// TestSelectManifestsByPlatformDoesNotCrowdOutOtherPlatforms is the
// regression this design specifically avoids: a single combined sort
// across every requested platform could rank two variants of one
// platform above the sole candidate for another, starving it of its
// slot. Selecting independently per platform must not do that.
func TestSelectManifestsByPlatformDoesNotCrowdOutOtherPlatforms(t *testing.T) {
	plain, erofs := plainAndErofsManifestDescs()
	arm64 := ocispec.Descriptor{Digest: digest.FromString("arm64"), Platform: &arm64Spec}
	children := []ocispec.Descriptor{plain, erofs, arm64}

	selected := selectManifestsByPlatform(children, []ocispec.Platform{amd64ErofsSpec, arm64Spec})
	require.Equal(t, 2, selected)
	assert.ElementsMatch(t, []digest.Digest{erofs.Digest, arm64.Digest}, digestsOf(children[:selected]))
}

// TestSelectManifestsByPlatformExactMatchIgnoresSubPlatform pins the
// reason platforms.Ordered(spec) - not platforms.Only(spec) - is used per
// spec: Only(linux/amd64) also accepts a linux/386 manifest as a lesser
// substitute, which would make list order (rather than architecture)
// decide the outcome if 386 happened to sort first. An exact match
// eliminates that case structurally: 386 is never selected for an amd64
// request regardless of position.
func TestSelectManifestsByPlatformExactMatchIgnoresSubPlatform(t *testing.T) {
	i386 := ocispec.Descriptor{Digest: digest.FromString("386"), Platform: &i386Spec}
	amd64 := ocispec.Descriptor{Digest: digest.FromString("amd64"), Platform: &amd64Spec}
	children := []ocispec.Descriptor{i386, amd64}

	selected := selectManifestsByPlatform(children, []ocispec.Platform{amd64Spec})
	require.Equal(t, 1, selected)
	assert.Equal(t, amd64.Digest, children[0].Digest)
}

// TestSelectManifestsByPlatformPreservesAllChildren verifies that
// reordering children in place never adds or drops an entry: the
// selected prefix and the remaining tail together are exactly the
// original set.
func TestSelectManifestsByPlatformPreservesAllChildren(t *testing.T) {
	plain, erofs := plainAndErofsManifestDescs()
	arm64 := ocispec.Descriptor{Digest: digest.FromString("arm64"), Platform: &arm64Spec}
	children := []ocispec.Descriptor{plain, erofs, arm64}
	original := digestsOf(children)

	selectManifestsByPlatform(children, []ocispec.Platform{amd64ErofsSpec, arm64Spec})

	assert.ElementsMatch(t, original, digestsOf(children))
}

func TestSelectManifestsPerPlatformKeepsOnlyBestVariant(t *testing.T) {
	plain, erofs := plainAndErofsManifestDescs()
	index := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageIndex, Digest: digest.FromString("index")}
	f := fakeChildrenHandler(map[digest.Digest][]ocispec.Descriptor{
		index.Digest: {plain, erofs},
	})

	got, err := SelectManifestsPerPlatform(f, []ocispec.Platform{amd64ErofsSpec})(context.Background(), index)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, erofs.Digest, got[0].Digest)
}

func TestSelectManifestsPerPlatformKeepsEveryDistinctPlatform(t *testing.T) {
	amd64 := ocispec.Descriptor{Digest: digest.FromString("amd64"), Platform: &amd64Spec}
	arm64 := ocispec.Descriptor{Digest: digest.FromString("arm64"), Platform: &arm64Spec}
	index := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageIndex, Digest: digest.FromString("index")}
	f := fakeChildrenHandler(map[digest.Digest][]ocispec.Descriptor{
		index.Digest: {amd64, arm64},
	})

	got, err := SelectManifestsPerPlatform(f, []ocispec.Platform{amd64Spec, arm64Spec})(context.Background(), index)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestSelectManifestsPerPlatformDropsNonMatching(t *testing.T) {
	amd64 := ocispec.Descriptor{Digest: digest.FromString("amd64"), Platform: &amd64Spec}
	arm64 := ocispec.Descriptor{Digest: digest.FromString("arm64"), Platform: &arm64Spec}
	index := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageIndex, Digest: digest.FromString("index")}
	f := fakeChildrenHandler(map[digest.Digest][]ocispec.Descriptor{
		index.Digest: {amd64, arm64},
	})

	got, err := SelectManifestsPerPlatform(f, []ocispec.Platform{amd64Spec})(context.Background(), index)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, amd64.Digest, got[0].Digest)
}

func TestSelectManifestsPerPlatformPassesThroughNonIndex(t *testing.T) {
	config := ocispec.Descriptor{Digest: digest.FromString("config")}
	layer := ocispec.Descriptor{Digest: digest.FromString("layer")}
	manifest := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromString("manifest")}
	f := fakeChildrenHandler(map[digest.Digest][]ocispec.Descriptor{
		manifest.Digest: {config, layer},
	})

	got, err := SelectManifestsPerPlatform(f, []ocispec.Platform{amd64Spec})(context.Background(), manifest)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestSelectManifestLayersPerPlatformPrunesNonBestVariantToConfig(t *testing.T) {
	plain, erofs := plainAndErofsManifestDescs()
	plainConfig := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromString("plain-config")}
	plainLayer := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString("plain-layer")}
	erofsConfig := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromString("erofs-config")}
	erofsLayer := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString("erofs-layer")}
	index := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageIndex, Digest: digest.FromString("index")}

	f := fakeChildrenHandler(map[digest.Digest][]ocispec.Descriptor{
		index.Digest: {plain, erofs},
		plain.Digest: {plainConfig, plainLayer},
		erofs.Digest: {erofsConfig, erofsLayer},
	})
	h := SelectManifestLayersPerPlatform(f, []ocispec.Platform{amd64ErofsSpec})

	ctx := context.Background()
	indexChildren, err := h(ctx, index)
	require.NoError(t, err)
	assert.Len(t, indexChildren, 2, "every manifest remains reachable for metadata")

	plainChildren, err := h(ctx, plain)
	require.NoError(t, err)
	require.Len(t, plainChildren, 1)
	assert.Equal(t, plainConfig.Digest, plainChildren[0].Digest, "the non-best variant is pruned to its config only")

	erofsChildren, err := h(ctx, erofs)
	require.NoError(t, err)
	assert.Len(t, erofsChildren, 2, "the best variant keeps its layers")
}

func TestSelectManifestLayersPerPlatformKeepsEachPlatformsLayers(t *testing.T) {
	amd64 := ocispec.Descriptor{Digest: digest.FromString("amd64"), Platform: &amd64Spec}
	amd64Config := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromString("amd64-config")}
	amd64Layer := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString("amd64-layer")}
	arm64 := ocispec.Descriptor{Digest: digest.FromString("arm64"), Platform: &arm64Spec}
	arm64Config := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromString("arm64-config")}
	arm64Layer := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString("arm64-layer")}
	index := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageIndex, Digest: digest.FromString("index")}

	f := fakeChildrenHandler(map[digest.Digest][]ocispec.Descriptor{
		index.Digest: {amd64, arm64},
		amd64.Digest: {amd64Config, amd64Layer},
		arm64.Digest: {arm64Config, arm64Layer},
	})
	h := SelectManifestLayersPerPlatform(f, []ocispec.Platform{amd64Spec, arm64Spec})

	ctx := context.Background()
	if _, err := h(ctx, index); err != nil {
		t.Fatal(err)
	}

	amd64Children, err := h(ctx, amd64)
	require.NoError(t, err)
	assert.Len(t, amd64Children, 2, "amd64 must keep its layers, not lose its slot to arm64")

	arm64Children, err := h(ctx, arm64)
	require.NoError(t, err)
	assert.Len(t, arm64Children, 2, "arm64 must keep its layers, not lose its slot to amd64")
}

func TestSelectManifestLayersPerPlatformLeavesStandaloneManifestUnfiltered(t *testing.T) {
	config := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.FromString("config")}
	layer := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.FromString("layer")}
	manifest := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromString("manifest"),
		Platform:  &arm64Spec,
	}
	f := fakeChildrenHandler(map[digest.Digest][]ocispec.Descriptor{
		manifest.Digest: {config, layer},
	})

	// A platform spec this manifest doesn't match: were it reachable
	// through an index, it would never even be a child of one. Since it
	// is the root descriptor - never observed as an index child at all -
	// it is not filtered at all.
	got, err := SelectManifestLayersPerPlatform(f, []ocispec.Platform{amd64Spec})(context.Background(), manifest)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}
