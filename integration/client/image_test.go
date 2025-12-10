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
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	. "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/defaults"
	imagelist "github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestImageIsUnpacked(t *testing.T) {
	imageName := imagelist.Get(imagelist.Pause)
	ctx, cancel := testContext(t)
	defer cancel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Cleanup
	opts := []images.DeleteOpt{images.SynchronousDelete()}
	err = client.ImageService().Delete(ctx, imageName, opts...)
	if err != nil && !errdefs.IsNotFound(err) {
		t.Fatal(err)
	}

	// By default pull does not unpack an image
	image, err := retryImagePull(ctx, t, client, imageName, WithPlatformMatcher(platforms.Default()))
	if err != nil {
		t.Fatal(err)
	}

	// Check that image is not unpacked
	unpacked, err := image.IsUnpacked(ctx, defaults.DefaultSnapshotter)
	if err != nil {
		t.Fatal(err)
	}
	if unpacked {
		t.Fatalf("image should not be unpacked")
	}

	// Check that image is unpacked
	err = image.Unpack(ctx, defaults.DefaultSnapshotter)
	if err != nil {
		t.Fatal(err)
	}
	unpacked, err = image.IsUnpacked(ctx, defaults.DefaultSnapshotter)
	if err != nil {
		t.Fatal(err)
	}
	if !unpacked {
		t.Fatalf("image should be unpacked")
	}
}

func TestImagePullWithDistSourceLabel(t *testing.T) {
	var (
		source   = "registry.k8s.io"
		repoName = "pause"
		tag      = "3.6"
	)

	ctx, cancel := testContext(t)
	defer cancel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	imageName := fmt.Sprintf("%s/%s:%s", source, repoName, tag)
	pMatcher := platforms.Default()

	// pull content without unpack and add distribution source label
	image, err := retryImagePull(ctx, t, client, imageName, WithPlatformMatcher(pMatcher))
	if err != nil {
		t.Fatal(err)
	}
	defer client.ImageService().Delete(ctx, imageName)

	cs := client.ContentStore()
	key := labels.LabelDistributionSource + "." + source

	// only check the target platform
	childrenHandler := images.LimitManifests(images.ChildrenHandler(cs), pMatcher, 1)

	checkLabelHandler := func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		children, err := childrenHandler(ctx, desc)
		if err != nil {
			return nil, err
		}

		info, err := cs.Info(ctx, desc.Digest)
		if err != nil {
			return nil, err
		}

		// check the label
		if got := info.Labels[key]; !strings.Contains(got, repoName) {
			return nil, fmt.Errorf("expected to have %s repo name in label, but got %s", repoName, got)
		}
		return children, nil
	}

	if err := images.Dispatch(ctx, images.HandlerFunc(checkLabelHandler), nil, image.Target()); err != nil {
		t.Fatal(err)
	}
}

func TestImageUsage(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	imageName := imagelist.Get(imagelist.Pause)
	ctx, cancel := testContext(t)
	defer cancel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Cleanup
	err = client.ImageService().Delete(ctx, imageName, images.SynchronousDelete())
	if err != nil && !errdefs.IsNotFound(err) {
		t.Fatal(err)
	}

	pMatcher := platforms.Default()

	// Pull single platform, do not unpack
	image, err := retryImagePull(ctx, t, client, imageName, WithPlatformMatcher(pMatcher))
	if err != nil {
		t.Fatal(err)
	}

	s1, err := image.Usage(ctx, WithUsageManifestLimit(1))
	if err != nil {
		t.Fatal(err)
	}

	// Pin image name to specific version for future fetches
	imageName = imageName + "@" + image.Target().Digest.String()
	defer client.ImageService().Delete(ctx, imageName, images.SynchronousDelete())

	// Fetch single platforms, but all manifests pulled
	if err := retryImageFetch(ctx, t, client, imageName, WithPlatformMatcher(pMatcher), WithAllMetadata()); err != nil {
		t.Fatal(err)
	}

	if s, err := image.Usage(ctx, WithUsageManifestLimit(1)); err != nil {
		t.Fatal(err)
	} else if s != s1 {
		t.Fatalf("unexpected usage %d, expected %d", s, s1)
	}

	s2, err := image.Usage(ctx, WithUsageManifestLimit(0))
	if err != nil {
		t.Fatal(err)
	}

	if s2 <= s1 {
		t.Fatalf("Expected larger usage counting all manifests: %d <= %d", s2, s1)
	}

	s3, err := image.Usage(ctx, WithUsageManifestLimit(0), WithManifestUsage())
	if err != nil {
		t.Fatal(err)
	}

	// When all manifests are pre-cached, s3 should be >= s2
	// (manifest reported sizes should be at least as large as actual usage)
	if s3 < s2 {
		t.Fatalf("Expected manifest reported usage to be >= actual usage: %d < %d", s3, s2)
	}

	// Fetch everything
	if err := retryImageFetch(ctx, t, client, imageName); err != nil {
		t.Fatal(err)
	}

	if s, err := image.Usage(ctx); err != nil {
		t.Fatal(err)
	} else if s != s3 {
		t.Fatalf("Expected actual usage to equal manifest reported usage of %d: got %d", s3, s)
	}

	err = image.Unpack(ctx, defaults.DefaultSnapshotter)
	if err != nil {
		t.Fatal(err)
	}

	if s, err := image.Usage(ctx, WithSnapshotUsage()); err != nil {
		t.Fatal(err)
	} else if s <= s3 {
		t.Fatalf("Expected actual usage with snapshots to be greater: %d <= %d", s, s3)
	}
}

func TestImageSupportedBySnapshotter_Error(t *testing.T) {
	var unsupportedImage string
	if runtime.GOOS == "windows" {
		unsupportedImage = "registry.k8s.io/pause-amd64:3.2"
	} else {
		unsupportedImage = "ghcr.io/containerd/windows/nanoserver:1809"
	}

	ctx, cancel := testContext(t)
	defer cancel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Cleanup
	err = client.ImageService().Delete(ctx, unsupportedImage)
	if err != nil && !errdefs.IsNotFound(err) {
		t.Fatal(err)
	}

	_, err = client.Pull(ctx, unsupportedImage,
		WithPlatform(platforms.DefaultString()),
		WithPullSnapshotter(defaults.DefaultSnapshotter),
		WithPullUnpack,
		WithUnpackOpts([]UnpackOpt{WithSnapshotterPlatformCheck()}),
	)

	if err == nil {
		t.Fatalf("expected unpacking %s for snapshotter %s to fail", unsupportedImage, defaults.DefaultSnapshotter)
	}
}

// retryImagePull provides retry logic for image pull operations with exponential backoff
func retryImagePull(ctx context.Context, t *testing.T, client *Client, imageName string, opts ...RemoteOpt) (Image, error) {
	var image Image
	var err error

	// Use a shorter timeout to allow multiple retry attempts within the overall test timeout
	retryCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	for i := 0; i < 3; i++ {
		image, err = client.Pull(retryCtx, imageName, opts...)
		if err == nil {
			return image, nil
		}
		if i < 2 {
			t.Logf("Pull attempt %d failed, retrying: %v", i+1, err)
			select {
			case <-retryCtx.Done():
				return nil, fmt.Errorf("context cancelled during retry: %w", retryCtx.Err())
			case <-time.After(time.Second * time.Duration(i+1)):
			}
		}
	}
	return nil, err
}

// retryImageFetch provides retry logic for image fetch operations with exponential backoff
func retryImageFetch(ctx context.Context, t *testing.T, client *Client, ref string, opts ...RemoteOpt) error {
	var err error

	// Use a shorter timeout to allow multiple retry attempts within the overall test timeout
	retryCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	for i := 0; i < 3; i++ {
		_, err = client.Fetch(retryCtx, ref, opts...)
		if err == nil {
			return nil
		}
		if i < 2 {
			t.Logf("Fetch attempt %d failed, retrying: %v", i+1, err)
			select {
			case <-retryCtx.Done():
				return fmt.Errorf("context cancelled during fetch retry: %w", retryCtx.Err())
			case <-time.After(time.Second * time.Duration(i+1)):
			}
		}
	}
	return err
}
