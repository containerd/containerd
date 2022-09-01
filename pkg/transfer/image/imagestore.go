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

package image

import (
	"context"
	"fmt"

	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/pkg/streaming"
	"github.com/containerd/containerd/pkg/transfer/plugins"
	"github.com/containerd/containerd/pkg/unpack"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/typeurl"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func init() {
	// TODO: Move this to separate package?
	plugins.Register(&transfertypes.ImageStore{}, &Store{}) // TODO: Rename ImageStoreDestination
}

type Store struct {
	// TODO: Put these configurations in object which can convert to/from any
	// Embed generated type
	imageName     string
	imageLabels   map[string]string
	platforms     platforms.MatchComparer
	allMetadata   bool
	labelMap      func(ocispec.Descriptor) []string
	manifestLimit int

	// TODO: Convert these to unpack platforms
	unpacks []unpack.Platform
}

func NewStore(image string) *Store {
	return &Store{
		imageName: image,
	}
}

func (is *Store) String() string {
	return fmt.Sprintf("Local Image Store (%s)", is.imageName)
}

func (is *Store) ImageFilter(h images.HandlerFunc, cs content.Store) images.HandlerFunc {
	h = images.SetChildrenMappedLabels(cs, h, is.labelMap)
	if is.allMetadata {
		// Filter manifests by platforms but allow to handle manifest
		// and configuration for not-target platforms
		h = remotes.FilterManifestByPlatformHandler(h, is.platforms)
	} else {
		// Filter children by platforms if specified.
		h = images.FilterPlatforms(h, is.platforms)
	}

	// Sort and limit manifests if a finite number is needed
	if is.manifestLimit > 0 {
		h = images.LimitManifests(h, is.platforms, is.manifestLimit)
	}
	return h
}

func (is *Store) Store(ctx context.Context, desc ocispec.Descriptor, store images.Store) (images.Image, error) {
	img := images.Image{
		Name:   is.imageName,
		Target: desc,
		Labels: is.imageLabels,
	}

	for {
		if created, err := store.Create(ctx, img); err != nil {
			if !errdefs.IsAlreadyExists(err) {
				return images.Image{}, err
			}

			updated, err := store.Update(ctx, img)
			if err != nil {
				// if image was removed, try create again
				if errdefs.IsNotFound(err) {
					continue
				}
				return images.Image{}, err
			}

			img = updated
		} else {
			img = created
		}

		return img, nil
	}
}

func (is *Store) Get(ctx context.Context, store images.Store) (images.Image, error) {
	return store.Get(ctx, is.imageName)
}

func (is *Store) UnpackPlatforms() []unpack.Platform {
	return is.unpacks
}

func (is *Store) MarshalAny(ctx context.Context, sm streaming.StreamCreator) (typeurl.Any, error) {
	s := &transfertypes.ImageStore{
		Name: is.imageName,
		// TODO: Support other fields
	}
	return typeurl.MarshalAny(s)
}

func (is *Store) UnmarshalAny(ctx context.Context, sm streaming.StreamGetter, a typeurl.Any) error {
	var s transfertypes.ImageStore
	if err := typeurl.UnmarshalTo(a, &s); err != nil {
		return err
	}

	is.imageName = s.Name
	// TODO: Support other fields

	return nil
}
