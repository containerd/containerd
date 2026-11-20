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

package overlay

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/continuity/fs"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/archive"
	"github.com/containerd/containerd/v2/pkg/archive/compression"
	"github.com/containerd/containerd/v2/pkg/epoch"
	"github.com/containerd/containerd/v2/pkg/labels"
)

func uniqueRef() string {
	t := time.Now()
	var b [3]byte
	// Ignore read failures, just decreases uniqueness
	rand.Read(b[:])
	return fmt.Sprintf("%d-%s", t.UnixNano(), base64.URLEncoding.EncodeToString(b[:]))
}

// getUpperDir only accepts a single overlay mount of the whole root.
// Multi-mount layouts or mounts at subdirectory targets need the walking
// differ, because an upperdir alone does not describe the whole tree.
func getUpperDir(mounts []mount.Mount) string {
	if len(mounts) != 1 || mounts[0].Type != "overlay" {
		return ""
	}
	m := mounts[0]
	if m.Target != "" && m.Target != "/" {
		return ""
	}

	var upper string
	for _, option := range m.Options {
		// A comma-joined or kernel-escaped option cannot be safely used as
		// a filesystem path without decoding. Fall back instead.
		if strings.ContainsAny(option, ",\\") {
			return ""
		}
		if strings.HasPrefix(option, "upperdir=") {
			if upper != "" {
				return ""
			}
			upper = strings.TrimPrefix(option, "upperdir=")
		}
		if strings.HasPrefix(option, "metacopy=") && option != "metacopy=off" {
			return ""
		}
		if strings.HasPrefix(option, "redirect_dir=") && option != "redirect_dir=off" {
			return ""
		}
	}
	return upper
}

// overlayLowerMatches proves that the requested lower view is exactly the
// overlay's lower layer. An overlay upperdir is not a diff against an arbitrary
// base, so ambiguous mounts must fall back to the walking differ.
func overlayLowerMatches(lower, upper []mount.Mount) bool {
	if len(lower) != 1 || len(upper) != 1 {
		return false
	}
	m := lower[0]
	if m.Target != "" && m.Target != "/" {
		return false
	}
	var upperLayers string
	for _, option := range upper[0].Options {
		if strings.HasPrefix(option, "lowerdir=") {
			if upperLayers != "" {
				return false
			}
			upperLayers = strings.TrimPrefix(option, "lowerdir=")
		}
	}
	if upperLayers == "" {
		return false
	}
	switch m.Type {
	case "bind":
		// Single bind-mounted lower directory, with no secondary layers.
		if strings.Contains(upperLayers, ":") || m.Source == "" {
			return false
		}
		return filepath.Clean(m.Source) == filepath.Clean(upperLayers)
	case "overlay":
		// Only a read-only overlay is eligible: an upperdir on this mount
		// could contribute additional data not described by lowerdir.
		var layers string
		for _, option := range m.Options {
			if strings.HasPrefix(option, "upperdir=") || strings.ContainsAny(option, ",\\") {
				return false
			}
			if strings.HasPrefix(option, "lowerdir=") {
				if layers != "" {
					return false
				}
				layers = strings.TrimPrefix(option, "lowerdir=")
			}
		}
		return layers != "" && layers == upperLayers
	default:
		return false
	}
}

// Both metacopy and redirect_dir can be enabled by module parameters even
// when absent from the mount options. Raw upperdirs cannot represent file
// data in metacopy files or the inherited children of redirected directories.
// Fail closed when the module parameters cannot be read.
func overlayFastPathFeaturesDisabled() bool {
	for _, feature := range []string{"metacopy", "redirect_dir"} {
		value, err := os.ReadFile("/sys/module/overlay/parameters/" + feature)
		if err != nil || !strings.EqualFold(strings.TrimSpace(string(value)), "N") {
			return false
		}
	}
	return true
}

// finalizeCompressedDiff must check Close because compressors may buffer
// bytes until finalization. Preserve the write error if both operations fail.
func finalizeCompressedDiff(compressed io.WriteCloser, writeErr error) error {
	return errors.Join(writeErr, compressed.Close())
}

// writeDiff writes the layer diff from an overlay upper directory to w.
// It uses fs.DiffDirChanges with DiffSourceOverlayFS to walk only the upperdir
// instead of traversing both the lower and upper directory trees. This avoids
// reading the (potentially large) lower layers entirely.
func writeDiff(ctx context.Context, w io.Writer, lower []mount.Mount, upperDir string, opts []archive.ChangeWriterOpt) error {
	return mount.WithTempMount(ctx, lower, func(lowerRoot string) error {
		cw := archive.NewChangeWriter(w, upperDir, opts...)
		err := fs.DiffDirChanges(ctx, lowerRoot, upperDir, fs.DiffSourceOverlayFS, cw.HandleChange)
		if err != nil {
			return fmt.Errorf("failed to create diff tar stream: %w", err)
		}
		return cw.Close()
	})
}

// Compare creates a diff between the given mounts and uploads the result
// to the content store. The upper mounts must be overlay-backed; otherwise
// errdefs.ErrNotImplemented is returned so the caller can fall back to a
// generic differ such as the walking differ.
//
// Note: ErrNotImplemented is intentionally used here (rather than
// ErrInvalidArgument) because plugins/services/diff/local.go uses
// errdefs.IsNotImplemented to decide whether to try the next differ in the
// ordered list. Returning any other error would stop the fallback chain and
// surface the error directly to the caller.
func (s *overlayDiff) Compare(ctx context.Context, lower, upper []mount.Mount, opts ...diff.Opt) (ocispec.Descriptor, error) {
	upperDir := getUpperDir(upper)
	if upperDir == "" || !overlayFastPathFeaturesDisabled() || !overlayLowerMatches(lower, upper) {
		return emptyDesc, fmt.Errorf("overlay fast path is not safe for these mounts: %w", errdefs.ErrNotImplemented)
	}

	var config diff.Config
	for _, opt := range opts {
		if err := opt(&config); err != nil {
			return emptyDesc, err
		}
	}
	if tm := epoch.FromContext(ctx); tm != nil && config.SourceDateEpoch == nil {
		config.SourceDateEpoch = tm
	}

	var compressionType compression.Compression
	if config.Compressor != nil {
		if config.MediaType == "" {
			return emptyDesc, errors.New("media type must be explicitly specified when using custom compressor")
		}
		compressionType = compression.Unknown
	} else {
		if config.MediaType == "" {
			config.MediaType = ocispec.MediaTypeImageLayerGzip
		}
		switch config.MediaType {
		case ocispec.MediaTypeImageLayer,
			images.MediaTypeDockerSchema2Layer,
			images.MediaTypeDockerSchema2LayerForeign:
			compressionType = compression.Uncompressed
		case ocispec.MediaTypeImageLayerGzip,
			images.MediaTypeDockerSchema2LayerGzip,
			images.MediaTypeDockerSchema2LayerForeignGzip:
			compressionType = compression.Gzip
		case ocispec.MediaTypeImageLayerZstd,
			images.MediaTypeDockerSchema2LayerZstd:
			compressionType = compression.Zstd
		default:
			return emptyDesc, fmt.Errorf("unsupported diff media type: %v: %w", config.MediaType, errdefs.ErrNotImplemented)
		}
	}

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
		return emptyDesc, fmt.Errorf("failed to open writer: %w", err)
	}

	// errOpen is set when an error occurs while the content writer has not been
	// committed or closed yet to force a cleanup.
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
		errOpen = finalizeCompressedDiff(compressed,
			writeDiff(ctx, io.MultiWriter(compressed, dgstr.Hash()), lower, upperDir, changeWriterOpts))
		if errOpen != nil {
			return emptyDesc, fmt.Errorf("failed to write compressed diff: %w", errOpen)
		}

		if config.Labels == nil {
			config.Labels = map[string]string{}
		}
		config.Labels[labels.LabelUncompressed] = dgstr.Digest().String()
	} else {
		if errOpen = writeDiff(ctx, cw, lower, upperDir, changeWriterOpts); errOpen != nil {
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
	// Set "containerd.io/uncompressed" label if digest already existed without label
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
