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
	"testing"

	. "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/converter"
	"github.com/containerd/containerd/v2/core/images/converter/uncompress"
	"github.com/containerd/platforms"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
)

// TestConvert creates an image from testImage, with the following conversion:
// - Media type: Docker -> OCI
// - Layer type: tar.gz -> tar
// - Arch:       Multi  -> Single
func TestConvert(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ctx, cancel := testContext(t)
	defer cancel()

	client, err := New(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err = client.Fetch(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	dstRef := testImage + "-testconvert"
	defPlat := platforms.DefaultStrict()
	opts := []converter.Opt{
		converter.WithDockerToOCI(true),
		converter.WithLayerConvertFunc(uncompress.LayerConvertFunc),
		converter.WithPlatform(defPlat),
	}
	dstImg, err := converter.Convert(ctx, client, dstRef, testImage, opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if deleteErr := client.ImageService().Delete(ctx, dstRef); deleteErr != nil {
			t.Fatal(deleteErr)
		}
	}()
	cs := client.ContentStore()
	plats, err := images.Platforms(ctx, cs, dstImg.Target)
	if err != nil {
		t.Fatal(err)
	}
	// Assert that the image does not have any extra arch.
	assert.Equal(t, 1, len(plats))
	assert.True(t, defPlat.Match(plats[0]))

	// Assert that the media type is converted to OCI and also uncompressed
	mani, err := images.Manifest(ctx, cs, dstImg.Target, defPlat)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range mani.Layers {
		assert.Equal(t, ocispec.MediaTypeImageLayer, l.MediaType)
	}
}
