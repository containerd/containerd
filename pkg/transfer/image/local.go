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

package transfer

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/pkg/streaming"
	"github.com/containerd/containerd/pkg/unpack"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/typeurl"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// TODO: Should a factory be exposed here as a service??
func NewOCIRegistryFromProto(p *transfer.OCIRegistry, resolver remotes.Resolver, sm streaming.StreamManager) *OCIRegistry {
	//transfer.OCIRegistry
	// Create resolver
	// Convert auth stream to credential manager
	return &OCIRegistry{
		reference: p.Reference,
		resolver:  resolver,
		streams:   sm,
	}
}

func NewOCIRegistry(ref string, resolver remotes.Resolver, sm streaming.StreamManager) *OCIRegistry {
	// With options, stream,
	// With streams?
	return &OCIRegistry{
		reference: ref,
		resolver:  resolver,
		streams:   sm,
	}
}

// OCI
type OCIRegistry struct {
	reference string

	resolver remotes.Resolver
	streams  streaming.StreamManager

	// This could be an interface which returns resolver?
	// Resolver could also be a plug-able interface, to call out to a program to fetch?
}

func (r *OCIRegistry) String() string {
	return fmt.Sprintf("OCI Registry (%s)", r.reference)
}

func (r *OCIRegistry) Image() string {
	return r.reference
}

func (r *OCIRegistry) Resolver() remotes.Resolver {
	return r.resolver
}

func (r *OCIRegistry) ToProto() typeurl.Any {
	// Might need more context to convert to proto
	// Need access to a stream manager
	// Service provider
	return nil
}

type ImageStore struct {
	// TODO: Put these configurations in object which can convert to/from any
	// Embed generated type
	imageName     string
	imageLabels   map[string]string
	platforms     platforms.MatchComparer
	allMetadata   bool
	labelMap      func(ocispec.Descriptor) []string
	manifestLimit int

	images  images.Store
	content content.Store

	// TODO: Convert these to unpack platforms
	unpacks []unpack.Platform
}

func NewImageStore(image string) *ImageStore {
	return &ImageStore{
		imageName: image,
	}
}

func (is *ImageStore) String() string {
	return fmt.Sprintf("Local Image Store (%s)", is.imageName)
}

func (is *ImageStore) FilterHandler(h images.HandlerFunc) images.HandlerFunc {
	h = images.SetChildrenMappedLabels(is.content, h, is.labelMap)
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

func (is *ImageStore) Store(ctx context.Context, desc ocispec.Descriptor) (images.Image, error) {
	img := images.Image{
		Name:   is.imageName,
		Target: desc,
		Labels: is.imageLabels,
	}

	for {
		if created, err := is.images.Create(ctx, img); err != nil {
			if !errdefs.IsAlreadyExists(err) {
				return images.Image{}, err
			}

			updated, err := is.images.Update(ctx, img)
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

func (is *ImageStore) UnpackPlatforms() []unpack.Platform {
	return is.unpacks
}

/*
type RemoteContext struct {
	// Resolver is used to resolve names to objects, fetchers, and pushers.
	// If no resolver is provided, defaults to Docker registry resolver.
	Resolver remotes.Resolver

	// PlatformMatcher is used to match the platforms for an image
	// operation and define the preference when a single match is required
	// from multiple platforms.
	PlatformMatcher platforms.MatchComparer

	// Unpack is done after an image is pulled to extract into a snapshotter.
	// It is done simultaneously for schema 2 images when they are pulled.
	// If an image is not unpacked on pull, it can be unpacked any time
	// afterwards. Unpacking is required to run an image.
	Unpack bool

	// UnpackOpts handles options to the unpack call.
	UnpackOpts []UnpackOpt

	// Snapshotter used for unpacking
	Snapshotter string

	// SnapshotterOpts are additional options to be passed to a snapshotter during pull
	SnapshotterOpts []snapshots.Opt

	// Labels to be applied to the created image
	Labels map[string]string

	// BaseHandlers are a set of handlers which get are called on dispatch.
	// These handlers always get called before any operation specific
	// handlers.
	BaseHandlers []images.Handler

	// HandlerWrapper wraps the handler which gets sent to dispatch.
	// Unlike BaseHandlers, this can run before and after the built
	// in handlers, allowing operations to run on the descriptor
	// after it has completed transferring.
	HandlerWrapper func(images.Handler) images.Handler

	// Platforms defines which platforms to handle when doing the image operation.
	// Platforms is ignored when a PlatformMatcher is set, otherwise the
	// platforms will be used to create a PlatformMatcher with no ordering
	// preference.
	Platforms []string

	// MaxConcurrentDownloads is the max concurrent content downloads for each pull.
	MaxConcurrentDownloads int

	// MaxConcurrentUploadedLayers is the max concurrent uploaded layers for each push.
	MaxConcurrentUploadedLayers int

	// AllMetadata downloads all manifests and known-configuration files
	AllMetadata bool

	// ChildLabelMap sets the labels used to reference child objects in the content
	// store. By default, all GC reference labels will be set for all fetched content.
	ChildLabelMap func(ocispec.Descriptor) []string
}
*/
/*
// What should streamhandler look like?
type StreamHandler interface {
	Authorize() error
	Progress(key string, int64)
}

// Distribution options
// Stream handler
// Progress rate
// Unpack options
// Remote options
// Cases:
//  Registry -> Content/ImageStore (pull)
//  Registry -> Registry
//  Content/ImageStore -> Registry (push)
//  Content/ImageStore -> Content/ImageStore (tag)
// Common fetch/push interface for registry, content/imagestore, OCI index
// Always starts with string for source and destination, on client side, does not need to resolve
//  Higher level implementation just takes strings and options
//  Lower level implementation takes pusher/fetcher?

*/
