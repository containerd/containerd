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

	"github.com/containerd/containerd/api/types"
	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/images/archive"
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
	imageName     string
	imageLabels   map[string]string
	platforms     []ocispec.Platform
	allMetadata   bool
	labelMap      func(ocispec.Descriptor) []string
	manifestLimit int

	//import image options
	namePrefix   string
	checkPrefix  bool
	digestRefs   bool
	alwaysDigest bool

	unpacks []UnpackConfiguration
}

// UnpackConfiguration specifies the platform and snapshotter to use for resolving
// the unpack Platform, if snapshotter is not specified the platform default will
// be used.
type UnpackConfiguration struct {
	Platform    ocispec.Platform
	Snapshotter string
}

// StoreOpt defines options when configuring an image store source or destination
type StoreOpt func(*Store)

// WithImageLabels are the image labels to apply to a new image
func WithImageLabels(labels map[string]string) StoreOpt {
	return func(s *Store) {
		s.imageLabels = labels
	}
}

// WithPlatforms specifies which platforms to fetch content for
func WithPlatforms(p ...ocispec.Platform) StoreOpt {
	return func(s *Store) {
		s.platforms = append(s.platforms, p...)
	}
}

// WithManifestLimit defines the max number of manifests to fetch
func WithManifestLimit(limit int) StoreOpt {
	return func(s *Store) {
		s.manifestLimit = limit
	}
}

func WithAllMetadata(s *Store) {
	s.allMetadata = true
}

// WithNamePrefix sets the name prefix for imported images, if
// check is enabled, then only images with the prefix are stored.
func WithNamePrefix(prefix string, check bool) StoreOpt {
	return func(s *Store) {
		s.namePrefix = prefix
		s.checkPrefix = check
	}
}

// WithDigestRefs sets digest refs for imported images, if
// always is enabled, then digest refs are added even if a
// non-digest image name is added for the same image.
func WithDigestRefs(always bool) StoreOpt {
	return func(s *Store) {
		s.digestRefs = true
		s.alwaysDigest = always
	}
}

// WithUnpack specifies a platform to unpack for and an optional snapshotter to use
func WithUnpack(p ocispec.Platform, snapshotter string) StoreOpt {
	return func(s *Store) {
		s.unpacks = append(s.unpacks, UnpackConfiguration{
			Platform:    p,
			Snapshotter: snapshotter,
		})
	}
}

// NewStore creates a new image store source or Destination
func NewStore(image string, opts ...StoreOpt) *Store {
	s := &Store{
		imageName: image,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

func (is *Store) String() string {
	return fmt.Sprintf("Local Image Store (%s)", is.imageName)
}

func (is *Store) ImageFilter(h images.HandlerFunc, cs content.Store) images.HandlerFunc {
	var p platforms.MatchComparer
	if len(is.platforms) == 0 {
		p = platforms.All
	} else {
		p = platforms.Ordered(is.platforms...)
	}
	h = images.SetChildrenMappedLabels(cs, h, is.labelMap)
	if is.allMetadata {
		// Filter manifests by platforms but allow to handle manifest
		// and configuration for not-target platforms
		h = remotes.FilterManifestByPlatformHandler(h, p)
	} else {
		// Filter children by platforms if specified.
		h = images.FilterPlatforms(h, p)
	}

	// Sort and limit manifests if a finite number is needed
	if is.manifestLimit > 0 {
		h = images.LimitManifests(h, p, is.manifestLimit)
	}
	return h
}

func (is *Store) Store(ctx context.Context, desc ocispec.Descriptor, store images.Store) (images.Image, error) {
	img := images.Image{
		Name:   is.imageName,
		Target: desc,
		Labels: is.imageLabels,
	}

	// Handle imported image names
	if refType, ok := desc.Annotations["io.containerd.import.ref-type"]; ok {
		var nameT func(string) string
		if is.checkPrefix {
			nameT = archive.FilterRefPrefix(is.namePrefix)
		} else {
			nameT = archive.AddRefPrefix(is.namePrefix)
		}
		name := imageName(desc.Annotations, nameT)
		switch refType {
		case "name":
			if name == "" {
				return images.Image{}, fmt.Errorf("no image name: %w", errdefs.ErrNotFound)
			}
			img.Name = name
		case "digest":
			if !is.digestRefs || (!is.alwaysDigest && name != "") {
				return images.Image{}, fmt.Errorf("no digest refs: %w", errdefs.ErrNotFound)
			}
			img.Name = fmt.Sprintf("%s@%s", is.namePrefix, desc.Digest)
		default:
			return images.Image{}, fmt.Errorf("ref type not supported: %w", errdefs.ErrInvalidArgument)
		}
		delete(desc.Annotations, "io.containerd.import.ref-type")
	} else if img.Name == "" {
		// No valid image combination found
		return images.Image{}, fmt.Errorf("no image name found: %w", errdefs.ErrNotFound)
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
	unpacks := make([]unpack.Platform, len(is.unpacks))
	for i, uc := range is.unpacks {
		unpacks[i].SnapshotterKey = uc.Snapshotter
		unpacks[i].Platform = platforms.Only(uc.Platform)
	}
	return unpacks
}

func (is *Store) MarshalAny(context.Context, streaming.StreamCreator) (typeurl.Any, error) {
	//unpack.Platform
	s := &transfertypes.ImageStore{
		Name:          is.imageName,
		Labels:        is.imageLabels,
		ManifestLimit: uint32(is.manifestLimit),
		AllMetadata:   is.allMetadata,
		Platforms:     platformsToProto(is.platforms),
		Prefix:        is.namePrefix,
		CheckPrefix:   is.checkPrefix,
		DigestRefs:    is.digestRefs,
		AlwaysDigest:  is.alwaysDigest,
		Unpacks:       unpackToProto(is.unpacks),
	}
	return typeurl.MarshalAny(s)
}

func (is *Store) UnmarshalAny(ctx context.Context, sm streaming.StreamGetter, a typeurl.Any) error {
	var s transfertypes.ImageStore
	if err := typeurl.UnmarshalTo(a, &s); err != nil {
		return err
	}

	is.imageName = s.Name
	is.imageLabels = s.Labels
	is.manifestLimit = int(s.ManifestLimit)
	is.allMetadata = s.AllMetadata
	is.platforms = platformFromProto(s.Platforms)
	is.namePrefix = s.Prefix
	is.checkPrefix = s.CheckPrefix
	is.digestRefs = s.DigestRefs
	is.alwaysDigest = s.AlwaysDigest
	is.unpacks = unpackFromProto(s.Unpacks)

	return nil
}

func platformsToProto(platforms []ocispec.Platform) []*types.Platform {
	ap := make([]*types.Platform, len(platforms))
	for i := range platforms {
		p := types.Platform{
			OS:           platforms[i].OS,
			Architecture: platforms[i].Architecture,
			Variant:      platforms[i].Variant,
		}

		ap[i] = &p
	}
	return ap
}

func platformFromProto(platforms []*types.Platform) []ocispec.Platform {
	op := make([]ocispec.Platform, len(platforms))
	for i := range platforms {
		op[i].OS = platforms[i].OS
		op[i].Architecture = platforms[i].Architecture
		op[i].Variant = platforms[i].Variant
	}
	return op
}

func unpackToProto(uc []UnpackConfiguration) []*transfertypes.UnpackConfiguration {
	auc := make([]*transfertypes.UnpackConfiguration, len(uc))
	for i := range uc {
		p := types.Platform{
			OS:           uc[i].Platform.OS,
			Architecture: uc[i].Platform.Architecture,
			Variant:      uc[i].Platform.Variant,
		}
		auc[i] = &transfertypes.UnpackConfiguration{
			Platform:    &p,
			Snapshotter: uc[i].Snapshotter,
		}
	}
	return auc
}

func unpackFromProto(auc []*transfertypes.UnpackConfiguration) []UnpackConfiguration {
	uc := make([]UnpackConfiguration, len(auc))
	for i := range auc {
		uc[i].Snapshotter = auc[i].Snapshotter
		if auc[i].Platform != nil {
			uc[i].Platform.OS = auc[i].Platform.OS
			uc[i].Platform.Architecture = auc[i].Platform.Architecture
			uc[i].Platform.Variant = auc[i].Platform.Variant
		}
	}
	return uc
}

func imageName(annotations map[string]string, ociCleanup func(string) string) string {
	name := annotations[images.AnnotationImageName]
	if name != "" {
		return name
	}
	name = annotations[ocispec.AnnotationRefName]
	if name != "" {
		if ociCleanup != nil {
			name = ociCleanup(name)
		}
	}
	return name
}
