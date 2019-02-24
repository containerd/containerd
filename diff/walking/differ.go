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

package walking

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"time"

	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/diff"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type walkingDiff struct {
	store content.Store
}

var emptyDesc = ocispec.Descriptor{}

const (
	// LabelUncompressed is set to the digest of uncompressed layer
	// for a compressed layer.
	LabelUncompressed = "containerd.io/uncompressed"
	// LabelEmptyLayer is set for an empty layer.
	// The value of the label is set to LabelValueEmptyLayer.
	// LabelEmptyLayer was introduced in containerd v1.3.
	LabelEmptyLayer = "containerd.io/empty-layer"
	// LabelValueEmptyLayer is the value used for LabelEmptyLayer
	LabelValueEmptyLayer = "true"
)

// NewWalkingDiff is a generic implementation of diff.Comparer.  The diff is
// calculated by mounting both the upper and lower mount sets and walking the
// mounted directories concurrently. Changes are calculated by comparing files
// against each other or by comparing file existence between directories.
// NewWalkingDiff uses no special characteristics of the mount sets and is
// expected to work with any filesystem.
func NewWalkingDiff(store content.Store) diff.Comparer {
	return &walkingDiff{
		store: store,
	}
}

// Compare creates a diff between the given mounts and uploads the result
// to the content store.
//
// The resulting blob may have labels such as LabelUncompressed and LabelEmptyLayer.
// These labels are not set to the annotation field of the descriptor.
func (s *walkingDiff) Compare(ctx context.Context, lower, upper []mount.Mount, opts ...diff.Opt) (d ocispec.Descriptor, err error) {
	var config diff.Config
	for _, opt := range opts {
		if err := opt(&config); err != nil {
			return emptyDesc, err
		}
	}

	if config.MediaType == "" {
		config.MediaType = ocispec.MediaTypeImageLayerGzip
	}
	if config.Labels == nil {
		config.Labels = map[string]string{}
	}

	var isCompressed bool
	switch config.MediaType {
	case ocispec.MediaTypeImageLayer:
	case ocispec.MediaTypeImageLayerGzip:
		isCompressed = true
	default:
		return emptyDesc, errors.Wrapf(errdefs.ErrNotImplemented, "unsupported diff media type: %v", config.MediaType)
	}

	var ocidesc ocispec.Descriptor
	if err := mount.WithTempMount(ctx, lower, func(lowerRoot string) error {
		return mount.WithTempMount(ctx, upper, func(upperRoot string) error {
			var newReference bool
			if config.Reference == "" {
				newReference = true
				config.Reference = uniqueRef()
			}

			cw, err := s.store.Writer(ctx,
				content.WithRef(config.Reference),
				content.WithDescriptor(ocispec.Descriptor{
					MediaType: config.MediaType, // most contentstore implementations just ignore this
				}))
			if err != nil {
				return errors.Wrap(err, "failed to open writer")
			}
			defer func() {
				if err != nil {
					cw.Close()
					if newReference {
						if err := s.store.Abort(ctx, config.Reference); err != nil {
							log.G(ctx).WithField("ref", config.Reference).Warnf("failed to delete diff upload")
						}
					}
				}
			}()
			if !newReference {
				if err := cw.Truncate(0); err != nil {
					return err
				}
			}

			var st archive.DiffStat
			onWriteDiffCompletion := func(v archive.DiffStat) {
				st = v
			}
			diffOpts := []archive.DiffOpt{archive.WithOnWriteDiffCompletion(onWriteDiffCompletion)}
			if isCompressed {
				dgstr := digest.SHA256.Digester()
				compressed, err := compression.CompressStream(cw, compression.Gzip)
				if err != nil {
					return errors.Wrap(err, "failed to get compressed stream")
				}
				err = archive.WriteDiff(ctx, io.MultiWriter(compressed, dgstr.Hash()), lowerRoot, upperRoot, diffOpts...)
				compressed.Close()
				if err != nil {
					return errors.Wrap(err, "failed to write compressed diff")
				}

				config.Labels[LabelUncompressed] = dgstr.Digest().String()
			} else {
				if err = archive.WriteDiff(ctx, cw, lowerRoot, upperRoot, diffOpts...); err != nil {
					return errors.Wrap(err, "failed to write diff")
				}
			}
			if st.Empty {
				config.Labels[LabelEmptyLayer] = LabelValueEmptyLayer
			}

			commitopts := []content.Opt{content.WithLabels(config.Labels)}
			dgst := cw.Digest()
			if err := cw.Commit(ctx, 0, dgst, commitopts...); err != nil {
				if !errdefs.IsAlreadyExists(err) {
					return errors.Wrap(err, "failed to commit")
				}
			}

			info, err := s.store.Info(ctx, dgst)
			if err != nil {
				return errors.Wrap(err, "failed to get info from content store")
			}

			// Set uncompressed label if digest already existed without label
			if _, ok := info.Labels[LabelUncompressed]; !ok {
				info.Labels[LabelUncompressed] = config.Labels[LabelUncompressed]
				if _, err := s.store.Update(ctx, info, "labels."+LabelUncompressed); err != nil {
					return errors.Wrap(err, "error setting uncompressed label")
				}
			}
			if st.Empty {
				// Set empty-layer label if digest already existed without label
				if _, ok := info.Labels[LabelEmptyLayer]; !ok {
					info.Labels[LabelEmptyLayer] = config.Labels[LabelEmptyLayer]
					if _, err := s.store.Update(ctx, info, "labels."+LabelEmptyLayer); err != nil {
						return errors.Wrapf(err, "error setting label %s", LabelEmptyLayer)
					}
				}
			}

			ocidesc = ocispec.Descriptor{
				MediaType: config.MediaType,
				Size:      info.Size,
				Digest:    info.Digest,
			}
			return nil
		})
	}); err != nil {
		return emptyDesc, err
	}

	return ocidesc, nil
}

func uniqueRef() string {
	t := time.Now()
	var b [3]byte
	// Ignore read failures, just decreases uniqueness
	rand.Read(b[:])
	return fmt.Sprintf("%d-%s", t.UnixNano(), base64.URLEncoding.EncodeToString(b[:]))
}
