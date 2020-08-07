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

package containerd

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/platforms"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestImageIsUnpacked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}

	const imageName = "docker.io/library/busybox:latest"
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
	image, err := client.Pull(ctx, imageName, WithPlatform("linux/amd64"))
	if err != nil {
		t.Fatal(err)
	}

	// Check that image is not unpacked
	unpacked, err := image.IsUnpacked(ctx, DefaultSnapshotter)
	if err != nil {
		t.Fatal(err)
	}
	if unpacked {
		t.Fatalf("image should not be unpacked")
	}

	// Check that image is unpacked
	err = image.Unpack(ctx, DefaultSnapshotter)
	if err != nil {
		t.Fatal(err)
	}
	unpacked, err = image.IsUnpacked(ctx, DefaultSnapshotter)
	if err != nil {
		t.Fatal(err)
	}
	if !unpacked {
		t.Fatalf("image should be unpacked")
	}
}

func TestImagePullWithDistSourceLabel(t *testing.T) {
	var (
		source   = "docker.io"
		repoName = "library/busybox"
		tag      = "latest"
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
	image, err := client.Pull(ctx, imageName, WithPlatformMatcher(pMatcher))
	if err != nil {
		t.Fatal(err)
	}
	defer client.ImageService().Delete(ctx, imageName)

	cs := client.ContentStore()
	key := fmt.Sprintf("containerd.io/distribution.source.%s", source)

	// only check the target platform
	childrenHandler := images.FilterPlatforms(images.ChildrenHandler(cs), pMatcher)

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
	if testing.Short() || runtime.GOOS == "windows" {
		t.Skip()
	}

	imageName := "docker.io/library/busybox:latest"
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

	testPlatform := platforms.Only(ocispec.Platform{
		OS:           "linux",
		Architecture: "amd64",
	})

	// Pull single platform, do not unpack
	image, err := client.Pull(ctx, imageName, WithPlatformMatcher(testPlatform))
	if err != nil {
		t.Fatal(err)
	}

	s1, err := image.Usage(ctx, WithUsageManifestLimit(1))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := image.Usage(ctx, WithUsageManifestLimit(0), WithManifestUsage()); err == nil {
		t.Fatal("expected NotFound with missing manifests")
	} else if !errdefs.IsNotFound(err) {
		t.Fatalf("unexpected error: %+v", err)
	}

	// Pin image name to specific version for future fetches
	imageName = imageName + "@" + image.Target().Digest.String()
	defer client.ImageService().Delete(ctx, imageName, images.SynchronousDelete())

	// Fetch single platforms, but all manifests pulled
	if _, err := client.Fetch(ctx, imageName, WithPlatformMatcher(testPlatform), WithAllMetadata()); err != nil {
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

	if s3 <= s2 {
		t.Fatalf("Expected larger usage counting all manifest reported sizes: %d <= %d", s3, s2)
	}

	// Fetch everything
	if _, err = client.Fetch(ctx, imageName); err != nil {
		t.Fatal(err)
	}

	if s, err := image.Usage(ctx); err != nil {
		t.Fatal(err)
	} else if s != s3 {
		t.Fatalf("Expected actual usage to equal manifest reported usage of %d: got %d", s3, s)
	}

	err = image.Unpack(ctx, DefaultSnapshotter)
	if err != nil {
		t.Fatal(err)
	}

	if s, err := image.Usage(ctx, WithSnapshotUsage()); err != nil {
		t.Fatal(err)
	} else if s <= s3 {
		t.Fatalf("Expected actual usage with snapshots to be greater: %d <= %d", s, s3)
	}
}
