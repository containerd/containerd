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
	"io"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/transfer"
	"github.com/containerd/containerd/pkg/unpack"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/typeurl"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
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
	case transfer.ImageImportStreamer:
		switch d := dest.(type) {
		case transfer.ImageExportStreamer:
			return ts.echo(ctx, s, d, topts)

			// Image import
			//  case transfer.ImageStorer
		}
	}
	return fmt.Errorf("unable to transfer from %s to %s: %w", name(src), name(dest), errdefs.ErrNotImplemented)
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

// echo is mostly used for testing, it implements an import->export which is
// a no-op which only roundtrips the bytes.
func (ts *localTransferService) echo(ctx context.Context, i transfer.ImageImportStreamer, e transfer.ImageExportStreamer, tops *transfer.TransferOpts) error {
	r, err := i.ImportStream(ctx)
	if err != nil {
		return err
	}
	wc, err := e.ExportStream(ctx)
	if err != nil {
		return err
	}

	// TODO: Use fixed buffer? Send write progress?
	_, err = io.Copy(wc, r)
	if werr := wc.Close(); werr != nil && err == nil {
		err = werr
	}
	return err
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
	if tops.Progress != nil {
		tops.Progress(transfer.Progress{
			Event: fmt.Sprintf("Resolving from %s", ir),
		})
	}

	name, desc, err := ir.Resolve(ctx)
	if err != nil {
		return fmt.Errorf("failed to resolve image: %w", err)
	}
	if desc.MediaType == images.MediaTypeDockerSchema1Manifest {
		// Explicitly call out schema 1 as deprecated and not supported
		return fmt.Errorf("schema 1 image manifests are no longer supported: %w", errdefs.ErrInvalidArgument)
	}

	// TODO: Handle already exists
	if tops.Progress != nil {
		tops.Progress(transfer.Progress{
			Event: fmt.Sprintf("Pulling from %s", ir),
		})
		tops.Progress(transfer.Progress{
			Event: "fetching image content",
			Name:  name,
			//Digest: img.Target.Digest.String(),
		})
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

		store           = ts.content
		progressTracker *ProgressTracker
	)

	if tops.Progress != nil {
		progressTracker = NewProgressTracker(name, store) //Pass in first name as root
		go progressTracker.HandleProgress(ctx, tops.Progress)
		defer progressTracker.Wait()
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

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

	// TODO: Allow initialization from configuration
	baseHandlers := []images.Handler{}

	if tops.Progress != nil {
		baseHandlers = append(baseHandlers, images.HandlerFunc(
			func(_ context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				progressTracker.Add(desc)

				return []ocispec.Descriptor{}, nil
			},
		))

		baseChildrenHandler := childrenHandler
		childrenHandler = images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) (children []ocispec.Descriptor, err error) {
			children, err = baseChildrenHandler(ctx, desc)
			if err != nil {
				return
			}
			progressTracker.AddChildren(desc, children)
			return
		})
	}

	handler = images.Handlers(append(baseHandlers,
		fetchHandler(store, fetcher, progressTracker),
		checkNeedsFix,
		childrenHandler, // List children to track hierachy
		appendDistSrcLabelHandler,
	)...)

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
			handler = unpacker.Unpack(handler)
		}
	}

	if err := images.Dispatch(ctx, handler, ts.limiter, desc); err != nil {
		if unpacker != nil {
			// wait for unpacker to cleanup
			unpacker.Wait()
		}
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

	img, err := is.Store(ctx, desc)
	if err != nil {
		return err
	}

	if tops.Progress != nil {
		tops.Progress(transfer.Progress{
			Event: "saved",
			Name:  img.Name,
			//Digest: img.Target.Digest.String(),
		})
	}

	if tops.Progress != nil {
		tops.Progress(transfer.Progress{
			Event: fmt.Sprintf("Completed pull from %s", ir),
		})
	}

	return nil
}

func fetchHandler(ingester content.Ingester, fetcher remotes.Fetcher, pt *ProgressTracker) images.HandlerFunc {
	return func(ctx context.Context, desc ocispec.Descriptor) (subdescs []ocispec.Descriptor, err error) {
		ctx = log.WithLogger(ctx, log.G(ctx).WithFields(logrus.Fields{
			"digest":    desc.Digest,
			"mediatype": desc.MediaType,
			"size":      desc.Size,
		}))

		switch desc.MediaType {
		case images.MediaTypeDockerSchema1Manifest:
			return nil, fmt.Errorf("%v not supported", desc.MediaType)
		default:
			err := remotes.Fetch(ctx, ingester, fetcher, desc)
			if errdefs.IsAlreadyExists(err) {
				pt.MarkExists(desc)
				return nil, nil
			}
			return nil, err
		}
	}
}
