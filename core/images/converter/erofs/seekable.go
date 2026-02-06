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

package erofs

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/converter"
	"github.com/containerd/containerd/v2/internal/erofsutils/seekable"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/containerd/v2/plugins/diff/erofs"
	"github.com/containerd/errdefs"
)

// SeekableConvertOpt configures seekable EROFS conversion.
type SeekableConvertOpt func(*seekableConvertOptions)

type seekableConvertOptions struct {
	chunkSizeBytes    *int
	includeDMVerity   bool
	dmVerityBlockSize *int
	// Options passed through to the underlying raw EROFS conversion.
	rawErofsOpts []ConvertOpt
}

// WithSeekableChunkSize sets the uncompressed bytes per zstd frame.
func WithSeekableChunkSize(size int) SeekableConvertOpt {
	return func(opts *seekableConvertOptions) {
		opts.chunkSizeBytes = &size
	}
}

// WithSeekableDMVerity enables dm-verity payload at EOF.
func WithSeekableDMVerity(enabled bool) SeekableConvertOpt {
	return func(opts *seekableConvertOptions) {
		opts.includeDMVerity = enabled
	}
}

// WithSeekableDMVerityBlockSize sets the dm-verity block size in bytes.
// If not set, the default (4096) is used.
func WithSeekableDMVerityBlockSize(size int) SeekableConvertOpt {
	return func(opts *seekableConvertOptions) {
		opts.dmVerityBlockSize = &size
	}
}

// WithSeekableRawErofsOpts passes options through to the raw EROFS conversion step.
func WithSeekableRawErofsOpts(opts ...ConvertOpt) SeekableConvertOpt {
	return func(o *seekableConvertOptions) {
		o.rawErofsOpts = append(o.rawErofsOpts, opts...)
	}
}

// SeekableLayerConvertFunc converts layers into seekable EROFS blobs.
//
// The conversion flow:
//  1. If the layer is already seekable EROFS (+zstd): no-op
//  2. If the layer is raw EROFS: wrap in zstd frames with chunk table
//  3. If the layer is tar-based: convert to raw EROFS first, then wrap
//
// The resulting blob uses MediaTypeErofsLayerZstd and carries annotations
// for chunk table offset, dm-verity offset, and root digest.
func SeekableLayerConvertFunc(opts ...SeekableConvertOpt) converter.ConvertFunc {
	return func(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
		var convertOpts seekableConvertOptions
		for _, opt := range opts {
			opt(&convertOpts)
		}

		if !images.IsLayerType(desc.MediaType) {
			return nil, nil
		}

		// No-op if already seekable EROFS.
		if images.IsSeekableErofsMediaType(desc.MediaType) {
			return nil, nil
		}

		// Get reader for the original blob.
		blobReaderAt, err := cs.ReaderAt(ctx, desc)
		if err != nil {
			return nil, err
		}
		defer blobReaderAt.Close()

		var rawErofsReader io.ReadCloser

		if erofs.IsErofsMediaType(desc.MediaType) {
			// Already raw EROFS: use directly.
			rawErofsReader = io.NopCloser(io.NewSectionReader(blobReaderAt, 0, desc.Size))
		} else {
			// Tar-based layer: convert to raw EROFS first using the existing converter.
			rawDesc, err := LayerConvertFunc(convertOpts.rawErofsOpts...)(ctx, cs, desc)
			if err != nil {
				return nil, fmt.Errorf("convert to raw EROFS: %w", err)
			}
			if rawDesc == nil {
				return nil, fmt.Errorf("unexpectedly got nil descriptor from EROFS conversion")
			}

			rawReaderAt, err := cs.ReaderAt(ctx, *rawDesc)
			if err != nil {
				return nil, fmt.Errorf("open raw EROFS blob: %w", err)
			}
			rawErofsReader = struct {
				io.Reader
				io.Closer
			}{
				Reader: io.NewSectionReader(rawReaderAt, 0, rawDesc.Size),
				Closer: rawReaderAt,
			}
		}
		defer rawErofsReader.Close()

		// Prepare content writer for the new seekable EROFS blob.
		contentRef := fmt.Sprintf("seekable-erofs-convert-%s", desc.Digest)
		contentWriter, err := content.OpenWriter(ctx, cs, content.WithRef(contentRef))
		if err != nil {
			return nil, err
		}
		defer contentWriter.Close()
		if err := contentWriter.Truncate(0); err != nil {
			return nil, err
		}

		// Preserve labels from original.
		origInfo, err := cs.Info(ctx, desc.Digest)
		if err != nil {
			return nil, err
		}
		labelsMap := map[string]string{}
		for k, v := range origInfo.Labels {
			labelsMap[k] = v
		}
		delete(labelsMap, labels.LabelUncompressed)

		// Tee to compute uncompressed digest.
		digester := digest.Canonical.Digester()
		input := io.TeeReader(rawErofsReader, digester.Hash())

		chunkSizeBytes := seekable.DefaultChunkSize
		if convertOpts.chunkSizeBytes != nil {
			chunkSizeBytes = *convertOpts.chunkSizeBytes
		}
		dmVerityBlockSize := 0
		if convertOpts.dmVerityBlockSize != nil {
			dmVerityBlockSize = *convertOpts.dmVerityBlockSize
		} else if convertOpts.includeDMVerity {
			dmVerityBlockSize = seekable.DefaultDMVerityBlockSize
		}

		encodeRes, err := seekable.EncodeErofsImageTo(ctx, input, contentWriter, seekable.EncodeOptions{
			ChunkSizeBytes:    chunkSizeBytes,
			IncludeDMVerity:   convertOpts.includeDMVerity,
			DMVerityBlockSize: dmVerityBlockSize,
		})
		if err != nil {
			return nil, fmt.Errorf("seekable EROFS encode: %w", err)
		}
		labelsMap[labels.LabelUncompressed] = digester.Digest().String()

		if err := contentWriter.Commit(ctx, 0, "", content.WithLabels(labelsMap)); err != nil && !errdefs.IsAlreadyExists(err) {
			return nil, err
		}

		newDesc := desc
		newDesc.Digest = contentWriter.Digest()
		if committedInfo, infoErr := cs.Info(ctx, newDesc.Digest); infoErr == nil {
			newDesc.Size = committedInfo.Size
		} else {
			return nil, infoErr
		}
		newDesc.MediaType = images.MediaTypeErofsLayerZstd

		if newDesc.Annotations == nil {
			newDesc.Annotations = map[string]string{}
		}
		if encodeRes.ChunkTableOffset >= 0 {
			newDesc.Annotations[seekable.AnnotationChunkTableOffset] = strconv.FormatInt(encodeRes.ChunkTableOffset, 10)
		}
		if encodeRes.ChunkTableDigest != "" {
			newDesc.Annotations[seekable.AnnotationChunkDigest] = encodeRes.ChunkTableDigest
		}
		if encodeRes.HasDMVerity {
			newDesc.Annotations[seekable.AnnotationDMVerityOffset] = strconv.FormatInt(encodeRes.DMVerityOffset, 10)
			if encodeRes.DMVerityRootDigest != "" {
				newDesc.Annotations[seekable.AnnotationDMVerityRootDigest] = encodeRes.DMVerityRootDigest
			}
			// Note: Block size is not annotated; it's read from the dm-verity superblock at decode time.
		}

		return &newDesc, nil
	}
}
