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

package local

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/pkg/transfer"
	"github.com/containerd/containerd/pkg/unpack"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/typeurl"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/semaphore"
)

type localTransferService struct {
	leases  leases.Manager
	content content.Store

	// semaphore.NewWeighted(int64(rCtx.MaxConcurrentDownloads))
	limiter *semaphore.Weighted

	// TODO: Duplication suppressor
	// Metadata service (Or snapshotters, image, content)
	// Diff

	// Configuration
	//  - Max downloads
	//  - Max uploads

	// Supported platforms
	//  - Platform -> snapshotter defaults?
}

func NewTransferService(lm leases.Manager, cs content.Store) transfer.Transferer {
	return &localTransferService{
		leases:  lm,
		content: cs,
	}
}

func (ts *localTransferService) Transfer(ctx context.Context, src interface{}, dest interface{}, opts ...transfer.Opt) error {
	topts := &transfer.TransferOpts{}
	for _, opt := range opts {
		opt(topts)
	}

	// Convert Any to real source/destination

	// Figure out matrix of whether source destination combination is supported
	switch s := src.(type) {
	case transfer.ImageResolver:
		switch d := dest.(type) {
		case transfer.ImageStorer:
			return ts.pull(ctx, s, d, topts)
		}
	}
	return fmt.Errorf("Unable to transfer from %s to %s: %w", name(src), name(dest), errdefs.ErrNotImplemented)
}

func name(t interface{}) string {
	switch s := t.(type) {
	case fmt.Stringer:
		return s.String()
	case typeurl.Any:
		return s.GetTypeUrl()
	default:
		return fmt.Sprintf("%T", t)
	}
}

func (ts *localTransferService) pull(ctx context.Context, ir transfer.ImageResolver, is transfer.ImageStorer, tops *transfer.TransferOpts) error {
	// TODO: Attach lease if doesn't have one

	// From source, need
	//   - resolver
	//   - image name

	// From destination, need
	//   - Image labels
	//   - Unpack information
	//     - Platform to Snapshotter
	//   - Child label map
	//   - All metdata?

	name, desc, err := ir.Resolve(ctx)
	if err != nil {
		return fmt.Errorf("failed to resolve image: %w", err)
	}
	if desc.MediaType == images.MediaTypeDockerSchema1Manifest {
		// Explicitly call out schema 1 as deprecated and not supported
		return fmt.Errorf("schema 1 image manifests are no longer supported: %w", errdefs.ErrInvalidArgument)
	}

	fetcher, err := ir.Fetcher(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to get fetcher for %q: %w", name, err)
	}

	var (
		handler images.Handler

		unpacker *unpack.Unpacker

		// has a config media type bug (distribution#1622)
		hasMediaTypeBug1622 bool

		store = ts.content
	)
	//func (is *ImageStore) FilterHandler(h images.HandlerFunc) images.HandlerFunc {
	//func (is *ImageStore) Store(ctx context.Context, desc ocispec.Descriptor) (images.Image, error) {

	// Get all the children for a descriptor
	childrenHandler := images.ChildrenHandler(store)

	if f, ok := is.(transfer.ImageFilterer); ok {
		childrenHandler = f.ImageFilter(childrenHandler)
	}

	// Sort and limit manifests if a finite number is needed
	//if limit > 0 {
	//	childrenHandler = images.LimitManifests(childrenHandler, rCtx.PlatformMatcher, limit)
	//}

	checkNeedsFix := images.HandlerFunc(
		func(_ context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
			// set to true if there is application/octet-stream media type
			if desc.MediaType == docker.LegacyConfigMediaType {
				hasMediaTypeBug1622 = true
			}

			return []ocispec.Descriptor{}, nil
		},
	)

	appendDistSrcLabelHandler, err := docker.AppendDistributionSourceLabel(store, name)
	if err != nil {
		return err
	}

	// TODO: Support set of base handlers from configuration or image store
	// Progress handlers?
	handlers := []images.Handler{
		remotes.FetchHandler(store, fetcher),
		checkNeedsFix,
		childrenHandler,
		appendDistSrcLabelHandler,
	}

	handler = images.Handlers(handlers...)

	// TODO: Should available platforms be a configuration of the service?
	// First find suitable platforms to unpack into
	//if unpacker, ok := is.
	if iu, ok := is.(transfer.ImageUnpacker); ok {
		unpacks := iu.UnpackPlatforms()
		if len(unpacks) > 0 {
			uopts := []unpack.UnpackerOpt{}
			for _, u := range unpacks {
				uopts = append(uopts, unpack.WithUnpackPlatform(u))
			}
			if ts.limiter != nil {
				uopts = append(uopts, unpack.WithLimiter(ts.limiter))
			}
			//if uconfig.DuplicationSuppressor != nil {
			//	uopts = append(uopts, unpack.WithDuplicationSuppressor(uconfig.DuplicationSuppressor))
			//}
			unpacker, err = unpack.NewUnpacker(ctx, ts.content, uopts...)
			if err != nil {
				return fmt.Errorf("unable to initialize unpacker: %w", err)
			}
			defer func() {
				// TODO: This needs to be tigher scoped...
				if _, err := unpacker.Wait(); err != nil {
					//if retErr == nil {
					//	retErr = fmt.Errorf("unpack: %w", err)
					//}
				}
			}()
			handler = unpacker.Unpack(handler)
		}
	}

	if err := images.Dispatch(ctx, handler, ts.limiter, desc); err != nil {
		// TODO: Cancel unpack and wait?
		return err
	}

	// NOTE(fuweid): unpacker defers blobs download. before create image
	// record in ImageService, should wait for unpacking(including blobs
	// download).
	if unpacker != nil {
		if _, err = unpacker.Wait(); err != nil {
			return err
		}
		// TODO: Check results to make sure unpack was successful
	}

	if hasMediaTypeBug1622 {
		if desc, err = docker.ConvertManifest(ctx, store, desc); err != nil {
			return err
		}
	}

	_, err = is.Store(ctx, desc)
	if err != nil {
		return err
	}

	// TODO: Send status update for created image

	return nil
}
