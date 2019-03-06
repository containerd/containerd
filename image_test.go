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
	ctx, cancel := testContext()
	defer cancel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Cleanup
	err = client.ImageService().Delete(ctx, imageName)
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

	ctx, cancel := testContext()
	defer cancel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	imageName := fmt.Sprintf("%s/%s:%s", source, repoName, tag)
	pMatcher := platforms.Default()

	// pull content without unpack and add distribution source label
	image, err := client.Pull(ctx, imageName,
		WithPlatformMatcher(pMatcher),
		WithAppendDistributionSourceLabel())
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
