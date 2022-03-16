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

	ttypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/pkg/transfer"
	"github.com/containerd/containerd/pkg/unpack"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/typeurl"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/semaphore"
)

func init() {

	plugin.Register(&plugin.Registration{
		Type: plugin.TransferPlugin,
		ID:   "image",
		Requires: []plugin.Type{
			plugin.LeasePlugin,
			plugin.MetadataPlugin,
		},
		Config: &transferConfig{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			m, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			ms := m.(*metadata.DB)
			l, err := ic.Get(plugin.LeasePlugin)
			if err != nil {
				return nil, err
			}

			// Map to url instance handler (typeurl.Any) interface{}

			return &localTransferService{
				leases:      l.(leases.Manager),
				content:     ms.ContentStore(),
				conversions: map[string]func(typeurl.Any) (interface{}, error){},
				//	// semaphore.NewWeighted(int64(rCtx.MaxConcurrentDownloads))
				//	limiter *semaphore.Weighted
			}, nil
		},
	})

}

type transferConfig struct {
	// Max concurrent downloads
	// Snapshotter platforms
}

// TODO: Move this to a local package with constructor arguments...?
type localTransferService struct {
	leases  leases.Manager
	content content.Store

	// semaphore.NewWeighted(int64(rCtx.MaxConcurrentDownloads))
	limiter *semaphore.Weighted

	conversions map[string]func(typeurl.Any) (interface{}, error)

	// TODO: Duplication suppressor
	// Metadata service (Or snapshotters, image, content)
	// Diff

	// Configuration
	//  - Max downloads
	//  - Max uploads

	// Supported platforms
	//  - Platform -> snapshotter defaults?

	// Type Resolver, support registration... For Any Type URL -> Constructor
}

// populatedConversions is used to map the typeurls to instance converstion functions,
// since the typeurls are derived from instances rather than types, they are
// calculated at runtime and populate the converstion map.
// Static mapping or offloading conversion to another plugin is probably a more
// ideal long term solution.
func (ts *localTransferService) populateConversions() error {
	for _, c := range []struct {
		instance   interface{}
		conversion func(typeurl.Any) (interface{}, error)
	}{
		{ttypes.ImageStoreDestination{}, ts.convertImageStoreDestination},
		{ttypes.OCIRegistry{}, ts.convertOCIRegistry},
	} {
		u, err := typeurl.TypeURL(c.instance)
		if err != nil {
			return fmt.Errorf("unable to get type %T: %w", c.instance, err)
		}
		if _, ok := ts.conversions[u]; ok {
			return fmt.Errorf("duplicate typeurl mapping: %s for %T", u, c.instance)
		}
	}

	return nil
}

func (ts *localTransferService) convertImageStoreDestination(a typeurl.Any) (interface{}, error) {
	var dest ttypes.ImageStoreDestination
	if err := typeurl.UnmarshalTo(a, &dest); err != nil {
		return nil, err
	}
	return nil, nil
}

func (ts *localTransferService) convertOCIRegistry(a typeurl.Any) (interface{}, error) {
	var dest ttypes.OCIRegistry
	if err := typeurl.UnmarshalTo(a, &dest); err != nil {
		return nil, err
	}
	// TODO: Create credential callback

	return nil, nil
}

func (ts *localTransferService) resolveType(a typeurl.Any) (interface{}, error) {
	c, ok := ts.conversions[a.GetTypeUrl()]
	if !ok {
		return nil, fmt.Errorf("type %q not supported: %w", a.GetTypeUrl(), errdefs.ErrNotImplemented)
	}
	return c(a)
}

func (ts *localTransferService) Transfer(ctx context.Context, src interface{}, dest interface{}, opts ...transfer.Opt) error {
	topts := &transfer.TransferOpts{}
	for _, opt := range opts {
		opt(topts)
	}

	if a, ok := src.(typeurl.Any); ok {
		r, err := ts.resolveType(a)
		if err != nil {
			return err
		}
		src = r
	}

	if a, ok := dest.(typeurl.Any); ok {
		r, err := ts.resolveType(a)
		if err != nil {
			return err
		}
		dest = r
	}

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

	// Get all the children for a descriptor
	childrenHandler := images.ChildrenHandler(store)

	if hw, ok := is.(transfer.ImageFilterer); ok {
		childrenHandler = hw.ImageFilter(childrenHandler)
	}
	// TODO: Move these to image store
	//// TODO: This could easily be handled by having an ImageHandlerWrapper()
	//// Set any children labels for that content

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

	// TODO: Support set of base handlers from configuration
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
	if u, ok := is.(transfer.ImageUnpacker); ok {
		uopts := []unpack.UnpackerOpt{}
		for _, u := range u.UnpackPlatforms() {
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
	// TODO: Send status update

	return nil
}
