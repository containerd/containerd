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

package client

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"

	. "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/converter"
	"github.com/containerd/containerd/v2/core/images/converter/erofs"
	imagelist "github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// EROFS superblock magic (little-endian) at offset 1024.
var erofsSuperMagicLE = []byte{0xe2, 0xe1, 0xf5, 0xe0}

// zstd frame magic (little-endian).
var zstdMagicLE = []byte{0x28, 0xb5, 0x2f, 0xfd}

func skipIfNoMkfsErofs(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("mkfs.erofs"); err != nil {
		t.Skipf("mkfs.erofs not in PATH: %v", err)
	}
}

// erofsPlatformSpec returns the default platform with OSFeatures=["erofs"].
// It is used as the platform matcher so that both first-pass (features=[])
// and idempotent second-pass (features=[erofs]) conversions pass the index
// filter, and to locate the converted manifest inside the resulting index.
func erofsPlatformSpec() ocispec.Platform {
	p := platforms.DefaultSpec()
	p.OSFeatures = append(p.OSFeatures, "erofs")
	return p
}

// convertToErofs converts srcRef into dstRef using the erofs layer converter.
func convertToErofs(t *testing.T, ctx context.Context, client *Client, srcRef, dstRef, blobCompression string) *images.Image {
	t.Helper()
	var erofsOpts []erofs.ConvertOpt
	if blobCompression != "" {
		erofsOpts = append(erofsOpts, erofs.WithBlobCompression(blobCompression))
	}
	opts := []converter.Opt{
		converter.WithLayerConvertFunc(erofs.LayerConvertFunc(erofsOpts...)),
		converter.WithUpdateManifest(erofs.UpdateManifestPlatform),
		converter.WithPlatform(platforms.OnlyStrict(erofsPlatformSpec())),
	}
	img, err := converter.Convert(ctx, client, dstRef, srcRef, opts...)
	require.NoError(t, err, "converter.Convert")
	return img
}

// resolveManifest returns the manifest descriptor matching the erofs platform,
// following index -> manifest if necessary.
func resolveManifest(t *testing.T, ctx context.Context, cs content.Store, target ocispec.Descriptor) (ocispec.Descriptor, ocispec.Manifest) {
	t.Helper()
	erofsPlat := platforms.OnlyStrict(erofsPlatformSpec())
	desc := target
	if images.IsIndexType(desc.MediaType) {
		raw, err := content.ReadBlob(ctx, cs, desc)
		require.NoError(t, err)
		var idx ocispec.Index
		require.NoError(t, json.Unmarshal(raw, &idx))
		var found *ocispec.Descriptor
		for i := range idx.Manifests {
			m := idx.Manifests[i]
			if m.Platform != nil && erofsPlat.Match(*m.Platform) {
				found = &m
				break
			}
		}
		require.NotNil(t, found, "no manifest matching erofs platform in converted index")
		desc = *found
	}
	raw, err := content.ReadBlob(ctx, cs, desc)
	require.NoError(t, err)
	var mani ocispec.Manifest
	require.NoError(t, json.Unmarshal(raw, &mani))
	return desc, mani
}

// configOSFeatures reads the `os.features` field from the config blob.
func configOSFeatures(t *testing.T, ctx context.Context, cs content.Store, configDesc ocispec.Descriptor) []string {
	t.Helper()
	raw, err := content.ReadBlob(ctx, cs, configDesc)
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))
	fRaw, ok := m["os.features"]
	if !ok {
		return nil
	}
	var features []string
	require.NoError(t, json.Unmarshal(fRaw, &features))
	return features
}

func readLayerHead(t *testing.T, ctx context.Context, cs content.Store, desc ocispec.Descriptor, n int) []byte {
	t.Helper()
	ra, err := cs.ReaderAt(ctx, desc)
	require.NoError(t, err)
	defer ra.Close()
	buf := make([]byte, n)
	_, err = ra.ReadAt(buf, 0)
	require.NoError(t, err)
	return buf
}

func deleteImage(t *testing.T, ctx context.Context, client *Client, ref string) {
	t.Helper()
	if err := client.ImageService().Delete(ctx, ref, images.SynchronousDelete()); err != nil && !errdefs.IsNotFound(err) {
		t.Fatalf("delete %s: %v", ref, err)
	}
}

func TestConvertErofsRaw(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	skipIfNoMkfsErofs(t)

	ctx, cancel := testContext(t)
	defer cancel()

	client, err := newClient(t, address)
	require.NoError(t, err)
	defer client.Close()

	testImage := imagelist.Get(imagelist.Alpine)

	_, err = client.Fetch(ctx, testImage)
	require.NoError(t, err, "fetch %s", testImage)

	dstRef := testImage + "-erofs-raw"
	defer deleteImage(t, ctx, client, dstRef)
	dstImg := convertToErofs(t, ctx, client, testImage, dstRef, "")

	cs := client.ContentStore()
	manifestDesc, mani := resolveManifest(t, ctx, cs, dstImg.Target)

	require.NotNil(t, manifestDesc.Platform, "converted manifest descriptor has no Platform")
	assert.Contains(t, manifestDesc.Platform.OSFeatures, "erofs")
	// UpdateManifestPlatform must preserve arch/os/variant and only augment
	// OSFeatures. Compare against the *normalized* default because
	// platforms.Normalize() collapses default variants to the empty string
	// (e.g. arm64+v8 -> arm64+"" and amd64+v1 -> amd64+"") while keeping
	// non-default variants (arm64+v9, arm+v7, arm+v6) as-is — this is the
	// canonical equivalence used by containerd's matchers.
	wantPlat := platforms.Normalize(platforms.DefaultSpec())
	assert.Equal(t, wantPlat.Architecture, manifestDesc.Platform.Architecture, "Architecture must be preserved")
	assert.Equal(t, wantPlat.OS, manifestDesc.Platform.OS, "OS must be preserved")
	assert.Equal(t, wantPlat.Variant, manifestDesc.Platform.Variant, "Variant must match normalized default")

	require.NotEmpty(t, mani.Layers)
	for i, l := range mani.Layers {
		assert.Equalf(t, images.MediaTypeErofsLayer, l.MediaType, "layer[%d] media type", i)
		head := readLayerHead(t, ctx, cs, l, 1028)
		assert.Equalf(t, erofsSuperMagicLE, head[1024:1028], "layer[%d] missing EROFS magic @1024", i)
	}

	feats := configOSFeatures(t, ctx, cs, mani.Config)
	assert.Contains(t, feats, "erofs")
}

func TestConvertErofsZstd(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	skipIfNoMkfsErofs(t)

	ctx, cancel := testContext(t)
	defer cancel()

	client, err := newClient(t, address)
	require.NoError(t, err)
	defer client.Close()

	testImage := imagelist.Get(imagelist.Alpine)

	_, err = client.Fetch(ctx, testImage)
	require.NoError(t, err, "fetch %s", testImage)

	dstRef := testImage + "-erofs-zstd"
	defer deleteImage(t, ctx, client, dstRef)
	dstImg := convertToErofs(t, ctx, client, testImage, dstRef, "zstd")

	cs := client.ContentStore()
	manifestDesc, mani := resolveManifest(t, ctx, cs, dstImg.Target)
	require.NotNil(t, manifestDesc.Platform)
	assert.Contains(t, manifestDesc.Platform.OSFeatures, "erofs")

	expectedMT := images.MediaTypeErofsLayer + "+zstd"
	require.NotEmpty(t, mani.Layers)
	for i, l := range mani.Layers {
		assert.Equalf(t, expectedMT, l.MediaType, "layer[%d] media type", i)

		head := readLayerHead(t, ctx, cs, l, 4)
		assert.Equalf(t, zstdMagicLE, head, "layer[%d] missing zstd magic", i)

		info, err := cs.Info(ctx, l.Digest)
		require.NoError(t, err)
		uncompressed := info.Labels[labels.LabelUncompressed]
		assert.NotEmptyf(t, uncompressed, "layer[%d] missing %s label", i, labels.LabelUncompressed)
		// zstd compression must change the digest of the blob.
		assert.NotEqualf(t, l.Digest.String(), uncompressed,
			"layer[%d] compressed digest equals uncompressed digest", i)
	}

	feats := configOSFeatures(t, ctx, cs, mani.Config)
	assert.Contains(t, feats, "erofs")
}
