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
	"fmt"
	"io"
	"strings"

	"github.com/containerd/continuity/fs"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/archive"
	"github.com/containerd/containerd/v2/pkg/archive/compression"
	"github.com/containerd/containerd/v2/pkg/labels"
)

// getOverlayUpperDir extracts the upperdir from overlay mount options.
// Returns empty string if not an overlay mount or upperdir not found.
func getOverlayUpperDir(mounts []mount.Mount) string {
	if len(mounts) == 0 {
		return ""
	}
	m := mounts[len(mounts)-1]
	if m.Type != "overlay" {
		return ""
	}
	for _, o := range m.Options {
		if strings.HasPrefix(o, "upperdir=") {
			return strings.TrimPrefix(o, "upperdir=")
		}
	}
	return ""
}

// writeDiffOverlay writes diff using DiffDirChanges for overlay filesystems.
// This is faster than the naive approach because it only walks the overlay
// upper directory instead of walking both lower and upper directories.
func writeDiffOverlay(ctx context.Context, w io.Writer, lower []mount.Mount, upperDir string, opts []archive.ChangeWriterOpt) error {
	return mount.WithTempMount(ctx, lower, func(lowerRoot string) error {
		cw := archive.NewChangeWriter(w, upperDir, opts...)
		err := fs.DiffDirChanges(ctx, lowerRoot, upperDir, fs.DiffSourceOverlayFS, cw.HandleChange)
		if err != nil {
			return fmt.Errorf("failed to create diff tar stream: %w", err)
		}
		return cw.Close()
	})
}

// compareOverlay uses the overlay fast path for diff computation.
// Instead of walking both lower and upper directories, it only walks
// the overlay upper directory and uses DiffDirChanges to detect changes.
func (s *walkingDiff) compareOverlay(ctx context.Context, lower []mount.Mount, upperDir string, compressionType compression.Compression, config *diff.Config) (ocispec.Descriptor, error) {
	var newReference bool
	if config.Reference == "" {
		newReference = true
		config.Reference = uniqueRef()
	}

	cw, err := s.store.Writer(ctx,
		content.WithRef(config.Reference),
		content.WithDescriptor(ocispec.Descriptor{
			MediaType: config.MediaType,
		}))
	if err != nil {
		return emptyDesc, fmt.Errorf("failed to open writer: %w", err)
	}

	var errOpen error
	defer func() {
		if errOpen != nil {
			cw.Close()
			if newReference {
				if abortErr := s.store.Abort(ctx, config.Reference); abortErr != nil {
					log.G(ctx).WithError(abortErr).WithField("ref", config.Reference).Warnf("failed to delete diff upload")
				}
			}
		}
	}()
	if !newReference {
		if errOpen = cw.Truncate(0); errOpen != nil {
			return emptyDesc, errOpen
		}
	}

	var changeWriterOpts []archive.ChangeWriterOpt
	if config.SourceDateEpoch != nil {
		changeWriterOpts = append(changeWriterOpts, archive.WithModTimeUpperBound(*config.SourceDateEpoch))
	}

	if compressionType != compression.Uncompressed {
		dgstr := digest.SHA256.Digester()
		var compressed io.WriteCloser
		if config.Compressor != nil {
			compressed, errOpen = config.Compressor(cw, config.MediaType)
			if errOpen != nil {
				return emptyDesc, fmt.Errorf("failed to get compressed stream: %w", errOpen)
			}
		} else {
			compressed, errOpen = compression.CompressStream(cw, compressionType)
			if errOpen != nil {
				return emptyDesc, fmt.Errorf("failed to get compressed stream: %w", errOpen)
			}
		}
		errOpen = writeDiffOverlay(ctx, io.MultiWriter(compressed, dgstr.Hash()), lower, upperDir, changeWriterOpts)
		compressed.Close()
		if errOpen != nil {
			return emptyDesc, fmt.Errorf("failed to write compressed diff: %w", errOpen)
		}

		if config.Labels == nil {
			config.Labels = map[string]string{}
		}
		config.Labels[labels.LabelUncompressed] = dgstr.Digest().String()
	} else {
		if errOpen = writeDiffOverlay(ctx, cw, lower, upperDir, changeWriterOpts); errOpen != nil {
			return emptyDesc, fmt.Errorf("failed to write diff: %w", errOpen)
		}
	}

	var commitopts []content.Opt
	if config.Labels != nil {
		commitopts = append(commitopts, content.WithLabels(config.Labels))
	}

	dgst := cw.Digest()
	if errOpen = cw.Commit(ctx, 0, dgst, commitopts...); errOpen != nil {
		if !errdefs.IsAlreadyExists(errOpen) {
			return emptyDesc, fmt.Errorf("failed to commit: %w", errOpen)
		}
		errOpen = nil
	}

	info, err := s.store.Info(ctx, dgst)
	if err != nil {
		return emptyDesc, fmt.Errorf("failed to get info from content store: %w", err)
	}
	if info.Labels == nil {
		info.Labels = make(map[string]string)
	}
	if _, ok := info.Labels[labels.LabelUncompressed]; !ok {
		info.Labels[labels.LabelUncompressed] = config.Labels[labels.LabelUncompressed]
		if _, err := s.store.Update(ctx, info, "labels."+labels.LabelUncompressed); err != nil {
			return emptyDesc, fmt.Errorf("error setting uncompressed label: %w", err)
		}
	}

	return ocispec.Descriptor{
		MediaType: config.MediaType,
		Size:      info.Size,
		Digest:    info.Digest,
	}, nil
}
