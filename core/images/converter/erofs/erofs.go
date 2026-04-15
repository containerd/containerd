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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/converter"
	"github.com/containerd/containerd/v2/core/images/converter/uncompress"
	"github.com/containerd/containerd/v2/internal/erofsutils"
	"github.com/containerd/containerd/v2/pkg/archive/compression"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
	"github.com/google/uuid"
)

type ConvertOpt func(*convertOptions)

type convertOptions struct {
	compressors     string
	mkfsExtraOpts   []string
	blobCompression string
}

func WithCompressors(compressors string) ConvertOpt {
	return func(opts *convertOptions) {
		opts.compressors = compressors
	}
}

func WithMkfsOptions(extraOpts []string) ConvertOpt {
	return func(opts *convertOptions) {
		opts.mkfsExtraOpts = extraOpts
	}
}

func WithBlobCompression(compression string) ConvertOpt {
	return func(opts *convertOptions) {
		opts.blobCompression = compression
	}
}

func LayerConvertFunc(opts ...ConvertOpt) converter.ConvertFunc {
	return func(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
		var convertOpts convertOptions
		for _, opt := range opts {
			opt(&convertOpts)
		}

		if !images.IsLayerType(desc.MediaType) || erofsutils.IsErofsMediaType(desc.MediaType) {
			return nil, nil
		}

		if images.IsNonDistributable(desc.MediaType) {
			return nil, nil
		}

		uncompressedDesc := &desc
		if !uncompress.IsUncompressedType(desc.MediaType) {
			var err error
			uncompressedDesc, err = uncompress.LayerConvertFunc(ctx, cs, desc)
			if err != nil {
				return nil, err
			}
			if uncompressedDesc == nil {
				return nil, fmt.Errorf("unexpectedly got the same blob after decompression (%s, %q)", desc.Digest, desc.MediaType)
			}
			log.G(ctx).Debugf("uncompressed %s into %s", desc.Digest, uncompressedDesc.Digest)
		}

		info, err := cs.Info(ctx, desc.Digest)
		if err != nil {
			return nil, fmt.Errorf("failed to get content info: %w", err)
		}

		labelz := info.Labels
		if labelz == nil {
			labelz = make(map[string]string)
		}

		ra, err := cs.ReaderAt(ctx, *uncompressedDesc)
		if err != nil {
			return nil, fmt.Errorf("failed to get reader: %w", err)
		}
		defer ra.Close()

		sr := io.NewSectionReader(ra, 0, uncompressedDesc.Size)

		blob, err := os.CreateTemp("", "layer-*.erofs")
		if err != nil {
			return nil, fmt.Errorf("failed to create temp file: %w", err)
		}
		blobPath := blob.Name()
		blob.Close()

		defer func() {
			if err := os.Remove(blobPath); err != nil && !os.IsNotExist(err) {
				log.G(ctx).WithError(err).Warnf("failed to remove temp file %s", blobPath)
			}
		}()

		var mkfsArgs []string
		if convertOpts.compressors != "" {
			compressionArg := "-z" + convertOpts.compressors
			mkfsArgs = append(mkfsArgs, compressionArg)
			mkfsArgs = append(mkfsArgs, []string{"-C", "65536"}...)
		}
		mkfsArgs = append(mkfsArgs, convertOpts.mkfsExtraOpts...)

		mkfsArgs = erofsutils.AddDefaultMkfsOpts(mkfsArgs)

		u := uuid.NewSHA1(uuid.NameSpaceURL, []byte("erofs:blobs/"+desc.Digest))
		if err := erofsutils.ConvertTarErofs(ctx, sr, blobPath, u.String(), mkfsArgs); err != nil {
			return nil, fmt.Errorf("failed to convert to EROFS: %w", err)
		}
		log.G(ctx).Debugf("converted %s to EROFS", desc.Digest)

		erofsFile, err := os.Open(blobPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open converted file: %w", err)
		}
		defer erofsFile.Close()

		ref := fmt.Sprintf("convert-erofs-from-%s", desc.Digest)
		w, err := content.OpenWriter(ctx, cs, content.WithRef(ref))
		if err != nil {
			return nil, fmt.Errorf("failed to open content writer: %w", err)
		}
		defer w.Close()

		if err := w.Truncate(0); err != nil {
			return nil, fmt.Errorf("failed to truncate writer: %w", err)
		}

		mediaType := images.MediaTypeErofsLayer

		if convertOpts.blobCompression == "zstd" {
			zw, err := compression.CompressStream(w, compression.Zstd)
			if err != nil {
				return nil, fmt.Errorf("failed to create zstd compressor: %w", err)
			}
			digester := digest.Canonical.Digester()
			if _, err := io.Copy(io.MultiWriter(zw, digester.Hash()), erofsFile); err != nil {
				zw.Close()
				return nil, fmt.Errorf("failed to compress EROFS blob: %w", err)
			}
			if err := zw.Close(); err != nil {
				return nil, fmt.Errorf("failed to finalize zstd stream: %w", err)
			}
			labelz[labels.LabelUncompressed] = digester.Digest().String()
			mediaType = images.MediaTypeErofsLayer + "+" + convertOpts.blobCompression
		} else {
			if _, err = io.Copy(w, erofsFile); err != nil {
				return nil, fmt.Errorf("failed to copy to content store: %w", err)
			}
			labelz[labels.LabelUncompressed] = w.Digest().String()
		}

		if err = w.Commit(ctx, 0, w.Digest(), content.WithLabels(labelz)); err != nil && !errdefs.IsAlreadyExists(err) {
			return nil, fmt.Errorf("failed to commit: %w", err)
		}

		cInfo, err := cs.Info(ctx, w.Digest())
		if err != nil {
			return nil, fmt.Errorf("failed to get content info: %w", err)
		}

		newDesc := desc
		newDesc.MediaType = mediaType
		newDesc.Digest = w.Digest()
		newDesc.Size = cInfo.Size
		return &newDesc, nil
	}
}

func UpdateManifestPlatform(ctx context.Context, cs content.Store, originalDesc, convertedDesc ocispec.Descriptor) (*ocispec.Descriptor, error) {
	if !images.IsManifestType(convertedDesc.MediaType) {
		return nil, nil
	}

	var manifest ocispec.Manifest
	manifestLabels, err := converter.ReadJSON(ctx, cs, &manifest, convertedDesc)
	if err != nil {
		return nil, err
	}

	var platform ocispec.Platform
	if originalDesc.Platform != nil {
		platform = *originalDesc.Platform
	} else {
		configPlatform, err := images.ConfigPlatform(ctx, cs, manifest.Config)
		if err != nil {
			return nil, err
		}
		platform = configPlatform
	}

	normalized := platforms.Normalize(platform)
	if !slices.Contains(normalized.OSFeatures, "erofs") {
		normalized.OSFeatures = append(normalized.OSFeatures, "erofs")
		normalized = platforms.Normalize(normalized)
	}

	var cfg converter.DualConfig
	configLabels, err := converter.ReadJSON(ctx, cs, &cfg, manifest.Config)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(normalized.OSFeatures)
	if err != nil {
		return nil, err
	}
	cfg["os.features"] = (*json.RawMessage)(&b)
	newConfig, err := converter.WriteJSON(ctx, cs, &cfg, manifest.Config, configLabels)
	if err != nil {
		return nil, err
	}

	if manifestLabels == nil {
		manifestLabels = make(map[string]string)
	}
	converter.ClearGCLabels(manifestLabels, manifest.Config.Digest)
	manifestLabels["containerd.io/gc.ref.content.config"] = newConfig.Digest.String()
	manifest.Config = *newConfig

	newManifestDesc, err := converter.WriteJSON(ctx, cs, &manifest, convertedDesc, manifestLabels)
	if err != nil {
		return nil, err
	}
	newManifestDesc.Platform = &normalized
	return newManifestDesc, nil
}
