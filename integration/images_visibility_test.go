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

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	coreimages "github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestImageTagWithoutRegistryNotVisibleInCRI(t *testing.T) {
	baseImage := images.Get(images.BusyBox) // ghcr.io/containerd/busybox:1.36

	t.Logf("Pulling base image %s", baseImage)
	img, err := containerdClient.Pull(t.Context(), baseImage)
	require.NoError(t, err)

	t.Run("ShortTag_Hidden", func(t *testing.T) {
		shortTag := fmt.Sprintf("busybox:hidden-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		t.Logf("Tagging as short tag %s", shortTag)
		createTag(t, t.Context(), img, shortTag)

		t.Logf("Verifying short tag is not visible in CRI Status")
		criImage, err := imageService.ImageStatus(&runtime.ImageSpec{Image: shortTag})
		require.NoError(t, err)
		require.Nil(t, criImage, "Short tag should not be visible in CRI Status")

		t.Logf("Verifying short tag is not visible in CRI ListImages")
		criImages, err := imageService.ListImages(nil)
		require.NoError(t, err)
		require.False(t, containsTag(criImages, shortTag), "Short tag should not be listed in CRI ListImages")
	})

	t.Run("FullTag_Visible", func(t *testing.T) {
		fullTag := fmt.Sprintf("docker.io/library/busybox:visible-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		t.Logf("Tagging as full tag %s", fullTag)
		createTag(t, t.Context(), img, fullTag)

		t.Logf("Verifying full tag is visible in CRI Status")
		require.NoError(t, Eventually(func() (bool, error) {
			criImage, err := imageService.ImageStatus(&runtime.ImageSpec{Image: fullTag})
			if err != nil {
				return false, err
			}
			return criImage != nil, nil
		}, 100*time.Millisecond, 10*time.Second), "Image did not become visible in CRI status")

		t.Logf("Verifying RepoTags contains %s", fullTag)
		criImage, err := imageService.ImageStatus(&runtime.ImageSpec{Image: fullTag})
		require.NoError(t, err)
		require.NotNil(t, criImage)
		require.Contains(t, criImage.RepoTags, fullTag)

		t.Logf("Verifying full tag is visible in CRI ListImages")
		var criImages []*runtime.Image
		require.NoError(t, Eventually(func() (bool, error) {
			criImages, err = imageService.ListImages(nil)
			if err != nil {
				return false, err
			}
			return containsTag(criImages, fullTag), nil
		}, 100*time.Millisecond, 10*time.Second), "Image did not become listed in CRI images")
	})
}

// createTag programmatically creates a containerd image object for testing visibility without CRI.
// It automatically registers cleanup for both containerd and CRI to prevent residual state.
func createTag(t *testing.T, ctx context.Context, img containerd.Image, tagName string) {
	t.Helper()

	// Registering cleanup *before* Create ensures that even if creation fails midway,
	// or the test terminates abruptly, we still attempt to delete. Deleting a non-existent
	// image is safe due to errdefs.IsNotFound.
	t.Cleanup(func() {
		err := imageService.RemoveImage(&runtime.ImageSpec{Image: tagName})
		if err != nil && !errdefs.IsNotFound(err) {
			require.NoError(t, err)
		}
	})

	t.Cleanup(func() {
		err := containerdClient.ImageService().Delete(context.Background(), tagName)
		if err != nil && !errdefs.IsNotFound(err) {
			require.NoError(t, err)
		}
	})

	tagImage := coreimages.Image{
		Name:   tagName,
		Target: img.Target(),
	}
	_, err := containerdClient.ImageService().Create(ctx, tagImage)
	require.NoError(t, err)
}

// containsTag checks if a list of CRI images contains a specific tag.
func containsTag(images []*runtime.Image, tag string) bool {
	for _, img := range images {
		for _, t := range img.RepoTags {
			if t == tag {
				return true
			}
		}
	}
	return false
}
