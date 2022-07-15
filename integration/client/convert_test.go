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
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	. "github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/images/converter"
	"github.com/containerd/containerd/images/converter/uncompress"
	"github.com/containerd/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"golang.org/x/sync/errgroup"
)

// convertToUncompressedWithAppendedFooter converts a given layer to uncompressed tar
// format and appending a footer file, which changes the diff id of the original layer,
// simulates a conversion similar to the estargz format.
func convertUncompressWithAppendedFooter(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
	tarDesc, err := uncompress.LayerConvertFunc(ctx, cs, desc)
	if err != nil {
		return nil, err
	}

	info, err := cs.Info(ctx, tarDesc.Digest)
	if err != nil {
		return nil, err
	}
	readerAt, err := cs.ReaderAt(ctx, *tarDesc)
	if err != nil {
		return nil, err
	}
	defer readerAt.Close()
	sr := io.NewSectionReader(readerAt, 0, tarDesc.Size)
	ref := fmt.Sprintf("convert-uncompress-from-%s", tarDesc.Digest)
	w, err := content.OpenWriter(ctx, cs, content.WithRef(ref))
	if err != nil {
		return nil, err
	}
	defer w.Close()

	var footer bytes.Buffer
	tw := tar.NewWriter(&footer)
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     "appended_footer",
		Size:     0,
	}); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}

	n, err := io.Copy(w, io.MultiReader(sr, &footer))
	if err != nil {
		return nil, err
	}
	if err = w.Commit(ctx, 0, "", content.WithLabels(info.Labels)); err != nil && !errdefs.IsAlreadyExists(err) {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	newDesc := *tarDesc
	newDesc.Digest = w.Digest()
	newDesc.Size = n
	return &newDesc, nil
}

func testConvert(t *testing.T, layerConvertFunc converter.ConvertFunc) {
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

	defPlat := platforms.DefaultStrict()
	opts := []converter.Opt{
		converter.WithDockerToOCI(true),
		converter.WithLayerConvertFunc(layerConvertFunc),
		converter.WithPlatform(defPlat),
		converter.WithSingleflight(),
	}

	c, err := converter.New(client, opts...)
	if err != nil {
		t.Fatal(err)
	}

	eg := errgroup.Group{}

	// Loop to test that the conversion works well when executed concurrently.
	for i := 0; i < 5; i++ {
		func(i int) {
			eg.Go(func() error {
				dstRef := fmt.Sprintf("%s-testconvert-%d", testImage, i)
				dstImg, err := c.Convert(ctx, dstRef, testImage)
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
				actualDiffIDs := []digest.Digest{}
				for _, l := range mani.Layers {
					if plats[0].OS == "windows" {
						assert.Equal(t, ocispec.MediaTypeImageLayerNonDistributable, l.MediaType)
					} else {
						assert.Equal(t, ocispec.MediaTypeImageLayer, l.MediaType)
					}
					actualDiffID, err := images.GetDiffID(ctx, cs, l)
					if err != nil {
						t.Fatal(err)
					}
					actualDiffIDs = append(actualDiffIDs, actualDiffID)
				}

				// Assert that the diff ids in the config are equal to actual.
				configDesc, err := images.Config(ctx, cs, dstImg.Target, defPlat)
				if err != nil {
					t.Fatal(err)
				}
				expectedDiffIDs, err := images.RootFS(ctx, cs, configDesc)
				if err != nil {
					t.Fatal(err)
				}
				assert.Equal(t, expectedDiffIDs, actualDiffIDs)

				return nil
			})
		}(i)
	}

	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}
}

// TestConvert creates multiple images from testImage, with the following conversion:
// - Media type: Docker -> OCI
// - Layer type: tar.gz -> tar / tar with footer
// - Arch:       Multi  -> Single
func TestConvert(t *testing.T) {
	testConvert(t, uncompress.LayerConvertFunc)
	testConvert(t, convertUncompressWithAppendedFooter)
}
