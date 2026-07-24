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
	"testing"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestFilterImageListForDisplay(t *testing.T) {
	d1 := digest.FromString("target-1")
	d2 := digest.FromString("target-2")
	target1 := ocispec.Descriptor{Digest: d1, MediaType: ocispec.MediaTypeImageManifest}
	target2 := ocispec.Descriptor{Digest: d2, MediaType: ocispec.MediaTypeImageManifest}

	// CRI-style triple for one image (k8s.io namespace after pull).
	criTriple := []images.Image{
		{Name: "docker.io/library/centos:7", Target: target1},
		{Name: "docker.io/library/centos@" + d1.String(), Target: target1},
		{Name: d1.String(), Target: target1},
	}

	t.Run("default hides CRI aliases when tag exists", func(t *testing.T) {
		got := filterImageListForDisplay(criTriple, false)
		if len(got) != 1 {
			t.Fatalf("expected 1 ref, got %d: %#v", len(got), names(got))
		}
		if got[0].Name != "docker.io/library/centos:7" {
			t.Fatalf("expected tagged ref, got %q", got[0].Name)
		}
	})

	t.Run("all keeps every reference", func(t *testing.T) {
		got := filterImageListForDisplay(criTriple, true)
		if len(got) != 3 {
			t.Fatalf("expected 3 refs, got %d: %#v", len(got), names(got))
		}
	})

	t.Run("digest-only images remain visible", func(t *testing.T) {
		list := []images.Image{
			{Name: d2.String(), Target: target2},
			{Name: "registry.example.com/app@" + d2.String(), Target: target2},
		}
		got := filterImageListForDisplay(list, false)
		if len(got) != 2 {
			t.Fatalf("expected both non-tagged refs, got %d: %#v", len(got), names(got))
		}
	})

	t.Run("multiple tags for same digest are all kept", func(t *testing.T) {
		list := []images.Image{
			{Name: "docker.io/library/alpine:3.18", Target: target1},
			{Name: "docker.io/library/alpine:latest", Target: target1},
			{Name: "docker.io/library/alpine@" + d1.String(), Target: target1},
		}
		got := filterImageListForDisplay(list, false)
		if len(got) != 2 {
			t.Fatalf("expected 2 tags, got %d: %#v", len(got), names(got))
		}
		want := map[string]bool{
			"docker.io/library/alpine:3.18":   true,
			"docker.io/library/alpine:latest": true,
		}
		for _, img := range got {
			if !want[img.Name] {
				t.Fatalf("unexpected name %q", img.Name)
			}
		}
	})

	t.Run("preserves order across digests", func(t *testing.T) {
		list := []images.Image{
			{Name: "docker.io/library/centos:7", Target: target1},
			{Name: d1.String(), Target: target1},
			{Name: "docker.io/library/alpine:latest", Target: target2},
			{Name: d2.String(), Target: target2},
		}
		got := filterImageListForDisplay(list, false)
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d: %#v", len(got), names(got))
		}
		if got[0].Name != "docker.io/library/centos:7" || got[1].Name != "docker.io/library/alpine:latest" {
			t.Fatalf("unexpected order: %#v", names(got))
		}
	})
}

func names(list []images.Image) []string {
	out := make([]string, len(list))
	for i, img := range list {
		out[i] = img.Name
	}
	return out
}
