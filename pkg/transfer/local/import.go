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
	"encoding/json"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/pkg/transfer"
	"github.com/containerd/containerd/pkg/unpack"
)

func (ts *localTransferService) importStream(ctx context.Context, i transfer.ImageImporter, is transfer.ImageStorer, tops *transfer.Config) error {
	ctx, done, err := ts.withLease(ctx)
	if err != nil {
		return err
	}
	defer done(ctx)

	if tops.Progress != nil {
		tops.Progress(transfer.Progress{
			Event: "Importing",
		})
	}

	index, err := i.Import(ctx, ts.content)
	if err != nil {
		return err
	}

	var (
		descriptors []ocispec.Descriptor
		handler     images.Handler
		unpacker    *unpack.Unpacker
	)

	// If save index, add index
	descriptors = append(descriptors, index)

	var handlerFunc images.HandlerFunc = func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		// Only save images at top level
		if desc.Digest != index.Digest {
			return images.Children(ctx, ts.content, desc)
		}

		p, err := content.ReadBlob(ctx, ts.content, desc)
		if err != nil {
			return nil, err
		}

		var idx ocispec.Index
		if err := json.Unmarshal(p, &idx); err != nil {
			return nil, err
		}

		for _, m := range idx.Manifests {
			m1 := m
			m1.Annotations = mergeMap(m.Annotations, map[string]string{"io.containerd.import.ref-type": "name"})
			descriptors = append(descriptors, m1)

			// If add digest references, add twice
			m2 := m
			m2.Annotations = mergeMap(m.Annotations, map[string]string{"io.containerd.import.ref-type": "digest"})
			descriptors = append(descriptors, m2)
		}

		return idx.Manifests, nil
	}

	if f, ok := is.(transfer.ImageFilterer); ok {
		handlerFunc = f.ImageFilter(handlerFunc, ts.content)
	}

	if err := images.WalkNotEmpty(ctx, handlerFunc, index); err != nil {
		return err
	}

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
			unpacker, err := unpack.NewUnpacker(ctx, ts.content, uopts...)
			if err != nil {
				return fmt.Errorf("unable to initialize unpacker: %w", err)
			}
			handler = unpacker.Unpack(handlerFunc)
		}
	}

	for _, desc := range descriptors {
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

		img, err := is.Store(ctx, desc, ts.images)
		if err != nil {
			if errdefs.IsNotFound(err) {
				continue
			}
			return err
		}

		if tops.Progress != nil {
			tops.Progress(transfer.Progress{
				Event: "saved",
				Name:  img.Name,
			})
		}
	}

	if tops.Progress != nil {
		tops.Progress(transfer.Progress{
			Event: "Completed import",
		})
	}

	return nil
}

func mergeMap(m1, m2 map[string]string) map[string]string {
	merged := make(map[string]string, len(m1)+len(m2))
	for k, v := range m1 {
		merged[k] = v
	}
	for k, v := range m2 {
		merged[k] = v
	}
	return merged
}
