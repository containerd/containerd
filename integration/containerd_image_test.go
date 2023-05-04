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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/containerd/containerd/v2"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/namespaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// Test to test the CRI plugin should see image pulled into containerd directly.
func TestContainerdImage(t *testing.T) {
	var testImage = images.Get(images.BusyBox)
	ctx := context.Background()

	t.Logf("make sure the test image doesn't exist in the cri plugin")
	i, err := imageService.ImageStatus(&runtime.ImageSpec{Image: testImage})
	require.NoError(t, err)
	if i != nil {
		require.NoError(t, imageService.RemoveImage(&runtime.ImageSpec{Image: testImage}))
	}

	t.Logf("pull the image into containerd")
	_, err = containerdClient.Pull(ctx, testImage, containerd.WithPullUnpack, containerd.WithPullLabel("foo", "bar"))
	assert.NoError(t, err)
	defer func() {
		// Make sure the image is cleaned up in any case.
		if err := containerdClient.ImageService().Delete(ctx, testImage); err != nil {
			assert.True(t, errdefs.IsNotFound(err), err)
		}
		assert.NoError(t, imageService.RemoveImage(&runtime.ImageSpec{Image: testImage}))
	}()

	t.Logf("the image should be seen by the cri plugin")
	var id string
	checkImage := func() (bool, error) {
		img, err := imageService.ImageStatus(&runtime.ImageSpec{Image: testImage})
		if err != nil {
			return false, err
		}
		if img == nil {
			t.Logf("Image %q not show up in the cri plugin yet", testImage)
			return false, nil
		}
		id = img.Id
		img, err = imageService.ImageStatus(&runtime.ImageSpec{Image: id})
		if err != nil {
			return false, err
		}
		if img == nil {
			// We always generate image id as a reference first, it must
			// be ready here.
			return false, errors.New("can't reference image by id")
		}
		if len(img.RepoTags) != 1 {
			// RepoTags must have been populated correctly.
			return false, fmt.Errorf("unexpected repotags: %+v", img.RepoTags)
		}
		if img.RepoTags[0] != testImage {
			return false, fmt.Errorf("unexpected repotag %q", img.RepoTags[0])
		}
		return true, nil
	}
	require.NoError(t, Eventually(checkImage, 100*time.Millisecond, 10*time.Second))
	require.NoError(t, Consistently(checkImage, 100*time.Millisecond, time.Second))
	defer func() {
		t.Logf("image should still be seen by id if only tag get deleted")
		if err := containerdClient.ImageService().Delete(ctx, testImage); err != nil {
			assert.True(t, errdefs.IsNotFound(err), err)
		}
		assert.NoError(t, Consistently(func() (bool, error) {
			img, err := imageService.ImageStatus(&runtime.ImageSpec{Image: id})
			if err != nil {
				return false, err
			}
			return img != nil, nil
		}, 100*time.Millisecond, time.Second))
		t.Logf("image should be removed from the cri plugin if all references get deleted")
		if err := containerdClient.ImageService().Delete(ctx, id); err != nil {
			assert.True(t, errdefs.IsNotFound(err), err)
		}
		assert.NoError(t, Eventually(func() (bool, error) {
			img, err := imageService.ImageStatus(&runtime.ImageSpec{Image: id})
			if err != nil {
				return false, err
			}
			return img == nil, nil
		}, 100*time.Millisecond, 10*time.Second))
	}()

	t.Logf("the image should be marked as managed")
	imgByRef, err := containerdClient.GetImage(ctx, testImage)
	assert.NoError(t, err)
	assert.Equal(t, imgByRef.Labels()["io.cri-containerd.image"], "managed")

	t.Logf("the image id should be created and managed")
	imgByID, err := containerdClient.GetImage(ctx, id)
	assert.NoError(t, err)
	assert.Equal(t, imgByID.Labels()["io.cri-containerd.image"], "managed")

	t.Logf("the image should be labeled")
	img, err := containerdClient.GetImage(ctx, testImage)
	assert.NoError(t, err)
	assert.Equal(t, img.Labels()["foo"], "bar")

	t.Logf("should be able to start container with the image")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "containerd-image")

	cnConfig := ContainerConfig(
		"test-container",
		id,
		WithCommand("sleep", "300"),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)
	require.NoError(t, runtimeService.StartContainer(cn))
	checkContainer := func() (bool, error) {
		s, err := runtimeService.ContainerStatus(cn)
		if err != nil {
			return false, err
		}
		if s.Resources == nil || (s.Resources.Linux == nil && s.Resources.Windows == nil) {
			return false, fmt.Errorf("No Resource field in container status: %+v", s)
		}
		return s.GetState() == runtime.ContainerState_CONTAINER_RUNNING, nil
	}
	require.NoError(t, Eventually(checkContainer, 100*time.Millisecond, 10*time.Second))
	require.NoError(t, Consistently(checkContainer, 100*time.Millisecond, time.Second))
}

// Test image managed by CRI plugin shouldn't be affected by images in other namespaces.
func TestContainerdImageInOtherNamespaces(t *testing.T) {
	var testImage = images.Get(images.BusyBox)
	ctx := context.Background()

	t.Logf("make sure the test image doesn't exist in the cri plugin")
	i, err := imageService.ImageStatus(&runtime.ImageSpec{Image: testImage})
	require.NoError(t, err)
	if i != nil {
		require.NoError(t, imageService.RemoveImage(&runtime.ImageSpec{Image: testImage}))
	}

	t.Logf("pull the image into test namespace")
	namespacedCtx := namespaces.WithNamespace(ctx, "test")
	_, err = containerdClient.Pull(namespacedCtx, testImage, containerd.WithPullUnpack)
	assert.NoError(t, err)
	defer func() {
		// Make sure the image is cleaned up in any case.
		if err := containerdClient.ImageService().Delete(namespacedCtx, testImage); err != nil {
			assert.True(t, errdefs.IsNotFound(err), err)
		}
		assert.NoError(t, imageService.RemoveImage(&runtime.ImageSpec{Image: testImage}))
	}()

	t.Logf("cri plugin should not see the image")
	checkImage := func() (bool, error) {
		img, err := imageService.ImageStatus(&runtime.ImageSpec{Image: testImage})
		if err != nil {
			return false, err
		}
		return img == nil, nil
	}
	require.NoError(t, Consistently(checkImage, 100*time.Millisecond, time.Second))

	PodSandboxConfigWithCleanup(t, "sandbox", "test")
	EnsureImageExists(t, testImage)

	t.Logf("cri plugin should see the image now")
	img, err := imageService.ImageStatus(&runtime.ImageSpec{Image: testImage})
	require.NoError(t, err)
	assert.NotNil(t, img)

	t.Logf("remove the image from test namespace")
	require.NoError(t, containerdClient.ImageService().Delete(namespacedCtx, testImage))

	t.Logf("cri plugin should still see the image")
	checkImage = func() (bool, error) {
		img, err := imageService.ImageStatus(&runtime.ImageSpec{Image: testImage})
		if err != nil {
			return false, err
		}
		return img != nil, nil
	}
	assert.NoError(t, Consistently(checkImage, 100*time.Millisecond, time.Second))
}
