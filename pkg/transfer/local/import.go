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

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/transfer"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
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

	var descriptors []ocispec.Descriptor

	// If save index, add index
	descriptors = append(descriptors, index)

	var handler images.HandlerFunc = func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
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
			m.Annotations = mergeMap(m.Annotations, map[string]string{"io.containerd.import.ref-source": "annotation"})
			descriptors = append(descriptors, m)
		}

		return idx.Manifests, nil
	}

	if f, ok := is.(transfer.ImageFilterer); ok {
		handler = f.ImageFilter(handler, ts.content)
	}

	if err := images.WalkNotEmpty(ctx, handler, index); err != nil {
		// TODO: Handle Not Empty as a special case on the input
		return err
	}

	for _, desc := range descriptors {
		imgs, err := is.Store(ctx, desc, ts.images)
		if err != nil {
			if errdefs.IsNotFound(err) {
				log.G(ctx).Infof("No images store for %s", desc.Digest)
				continue
			}
			return err
		}

		if tops.Progress != nil {
			for _, img := range imgs {
				tops.Progress(transfer.Progress{
					Event: "saved",
					Name:  img.Name,
				})
			}
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
