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
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/continuity/fs"
	"github.com/containerd/continuity/sysx"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/moby/sys/mountinfo"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sys/unix"

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

// hasNonOverlayMountOperation rejects mount flags that cause mount.Mount to
// perform a bind or remount operation even when its Type says "overlay".
// Reading an upperdir from such a mount would not describe its mounted view.
func hasNonOverlayMountOperation(options []string) bool {
	return slices.Contains(options, "bind") ||
		slices.Contains(options, "rbind") ||
		slices.Contains(options, "remount")
}

// getUpperDir only accepts a single overlay mount of the whole root.
// Multi-mount layouts or mounts at subdirectory targets need the walking
// differ, because an upperdir alone does not describe the whole tree.
func getUpperDir(mounts []mount.Mount) string {
	if len(mounts) != 1 || mounts[0].Type != "overlay" {
		return ""
	}
	m := mounts[0]
	if (m.Target != "" && m.Target != "/") || hasNonOverlayMountOperation(m.Options) {
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
		// The raw upperdir has host ownership, not the ID-mapped mount view.
		// Use the walking differ if either mapping is present.
		if strings.HasPrefix(option, "uidmap=") || strings.HasPrefix(option, "gidmap=") {
			return ""
		}
		// The continuity fast path does not understand user.overlay whiteouts.
		// A userxattr mount must use the walking differ.
		if option == "userxattr" || strings.HasPrefix(option, "userxattr=") {
			return ""
		}
		if strings.HasPrefix(option, "metacopy=") && option != "metacopy=off" {
			return ""
		}
		if strings.HasPrefix(option, "redirect_dir=") && option != "redirect_dir=off" {
			return ""
		}
		// Indexed overlayfs can copy up a lower hardlink through an index
		// inode. The raw upperdir does not describe all visible link names.
		if strings.HasPrefix(option, "index=") && option != "index=off" {
			return ""
		}
		// NFS export requires an overlay index for writable mounts.
		if strings.HasPrefix(option, "nfs_export=") && option != "nfs_export=off" {
			return ""
		}
	}
	return upper
}

// overlayLowerMatches proves that the requested lower view is exactly the
// overlay's lower layer. An overlay upperdir is not a diff against an arbitrary
// base, so ambiguous mounts must fall back to the walking differ.
func overlayLowerMatches(lower, upper []mount.Mount) bool {
	if len(lower) != 1 || len(upper) != 1 || upper[0].Type != "overlay" ||
		hasNonOverlayMountOperation(upper[0].Options) {
		return false
	}
	m := lower[0]
	if m.Type == "overlay" && hasNonOverlayMountOperation(m.Options) {
		return false
	}
	if m.Target != "" && m.Target != "/" {
		return false
	}
	for _, option := range m.Options {
		if strings.HasPrefix(option, "uidmap=") || strings.HasPrefix(option, "gidmap=") ||
			option == "userxattr" || strings.HasPrefix(option, "userxattr=") {
			// Lower and upper must use the same whiteout namespace.
			// The raw-upperdir differ cannot prove this for userxattr mounts.
			return false
		}
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

// Metacopy, redirect_dir and index can be enabled by module parameters even
// when absent from mount options. Raw upperdirs cannot safely represent
// their inherited file data, redirected children or indexed hardlinks.
// Explicit feature=off overrides the kernel default; options that enable
// unsupported features are rejected earlier by getUpperDir.
// Fail closed when a required module parameter cannot be read.
func overlayFastPathFeaturesDisabled(options []string) bool {
	return overlayFastPathFeaturesDisabledWithReader(options, os.ReadFile)
}

func overlayFastPathFeaturesDisabledWithReader(options []string, readFile func(string) ([]byte, error)) bool {
	for _, feature := range []string{"metacopy", "redirect_dir", "index"} {
		if slices.Contains(options, feature+"=off") {
			continue
		}
		value, err := readFile("/sys/module/overlay/parameters/" + feature)
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

// hasOverlayXattrWhiteout recognizes the alternate overlayfs whiteout
// representation: an empty regular file with an overlay.whiteout xattr.
// DiffDirChanges only recognizes character-device whiteouts, and exporting
// such a file directly would produce an incorrect image layer.
func hasOverlayXattrWhiteout(path string) (bool, error) {
	for _, name := range []string{"user.overlay.whiteout", "trusted.overlay.whiteout"} {
		// A zero-length xattr is a valid whiteout: only its presence matters.
		_, err := unix.Lgetxattr(path, name, nil)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, unix.ENODATA), errors.Is(err, unix.ENOTSUP):
			continue
		default:
			// If the xattr cannot be inspected, fall back rather than
			// potentially exporting a whiteout as an ordinary file.
			return false, err
		}
	}
	return false, nil
}

func withOverlayWhiteoutFallback(upperDir string, handle fs.ChangeFunc) fs.ChangeFunc {
	return func(kind fs.ChangeKind, path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Continuity may interpret either trusted or user.overlay.opaque,
		// regardless of the mount's xattr namespace. A marker in the
		// inactive namespace can wrongly delete inherited files. Delegate
		// opaque directories to the mounted-view walking differ.
		if kind == fs.ChangeKindDelete && filepath.Base(path) == ".wh..opq" {
			return fmt.Errorf("overlay opaque directory %q requires walking differ: %w", path, errdefs.ErrNotImplemented)
		}
		if info != nil && info.Mode().IsRegular() {
			whiteout, checkErr := hasOverlayXattrWhiteout(filepath.Join(upperDir, path))
			if checkErr != nil {
				return fmt.Errorf("cannot inspect overlay whiteout %q: %v: %w", path, checkErr, errdefs.ErrNotImplemented)
			}
			if whiteout {
				return fmt.Errorf("overlay xattr whiteout %q requires walking differ: %w", path, errdefs.ErrNotImplemented)
			}
		}
		return handle(kind, path, info, nil)
	}
}

// hasOverlayRootOpacity detects opacity on the root of an upperdir. The
// continuity fast path skips the root directory, so it cannot emit an OCI
// opaque marker or the inherited-file deletions for a root-level opaque xattr.
// Be conservative if the attribute cannot be inspected.
func hasOverlayRootOpacity(upperDir string) (bool, error) {
	for _, name := range []string{"trusted.overlay.opaque", "user.overlay.opaque"} {
		_, err := unix.Lgetxattr(upperDir, name, nil)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, unix.ENODATA), errors.Is(err, unix.ENOTSUP):
			continue
		default:
			return false, err
		}
	}
	return false, nil
}

// hasNestedLowerMount reports mountpoints below the temporarily mounted lower
// root. A recursive bind can expose submounts that are not represented by the
// overlay's lowerdir paths, so comparing against its mounted view is unsafe.
func hasNestedLowerMount(root string, mounts []*mountinfo.Info) (nested, foundRoot bool) {
	root = filepath.Clean(root)
	prefix := root + string(os.PathSeparator)
	for _, entry := range mounts {
		if entry.Mountpoint == root {
			foundRoot = true
		} else if strings.HasPrefix(entry.Mountpoint, prefix) {
			nested = true
		}
	}
	return nested, foundRoot
}

// requireFlatLowerMount ensures that the temporary lower mount has no nested
// mountpoints that could change the view relative to the overlay lowerdir.
// Fail closed if mountinfo is unavailable or the temporary mount is not found.
func requireFlatLowerMount(root string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("cannot resolve lower mount %q: %v: %w", root, err, errdefs.ErrNotImplemented)
	}
	mounts, err := mountinfo.GetMounts(mountinfo.PrefixFilter(realRoot))
	if err != nil {
		return fmt.Errorf("cannot inspect nested lower mounts: %v: %w", err, errdefs.ErrNotImplemented)
	}
	nested, foundRoot := hasNestedLowerMount(realRoot, mounts)
	if nested || !foundRoot {
		return fmt.Errorf("lower mount %q has nested or unverified mountpoints: %w", realRoot, errdefs.ErrNotImplemented)
	}
	return nil
}

// requireSafeUpperDir checks metadata that continuity's DiffDirChanges does
// not compare. This walks only the upperdir; if a change cannot be represented
// safely, the service retries with the generic walking differ.
func requireSafeUpperDir(ctx context.Context, lowerRoot, upperDir string) error {
	return filepath.Walk(upperDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		relative, err := filepath.Rel(upperDir, path)
		if err != nil {
			return err
		}
		lowerPath := filepath.Join(lowerRoot, relative)
		lowerInfo, err := os.Lstat(lowerPath)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			// A character device with major/minor 0/0 is an overlay
			// whiteout, not an added device node. Without a matching
			// lower entry continuity would archive it as a device.
			if info.Mode()&os.ModeCharDevice != 0 {
				if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Rdev == 0 {
					return fmt.Errorf("orphan overlay whiteout %q needs walking differ: %w", relative, errdefs.ErrNotImplemented)
				}
			}
			return nil
		}
		if err != nil {
			return err
		}

		// os.Stat in the recorded continuity dependency follows lower
		// symlinks, including dangling links and directory ancestors.
		// This can misclassify a whiteout or read outside the lower tree.
		if lowerInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("lower symlink %q needs walking differ: %w", relative, errdefs.ErrNotImplemented)
		}

		if info.IsDir() && !lowerInfo.IsDir() {
			// A file-to-directory replacement makes continuity's later
			// base lookups return ENOTDIR on nested files.
			return fmt.Errorf("lower non-directory %q needs walking differ: %w", relative, errdefs.ErrNotImplemented)
		}
		if !info.IsDir() || !lowerInfo.IsDir() {
			return nil
		}

		upperStat, upperOK := info.Sys().(*syscall.Stat_t)
		lowerStat, lowerOK := lowerInfo.Sys().(*syscall.Stat_t)
		if !upperOK || !lowerOK || upperStat.Uid != lowerStat.Uid || upperStat.Gid != lowerStat.Gid {
			// Otherwise a directory-only ownership change is skipped
			// when size, mode and mtime happen to match.
			return fmt.Errorf("directory ownership %q needs walking differ: %w", relative, errdefs.ErrNotImplemented)
		}

		// The fast directory comparison also ignores security.capability.
		// If the xattr differs, let the walking differ emit the change.
		upperCap, upperErr := directoryCapability(path)
		lowerCap, lowerErr := directoryCapability(lowerPath)
		if upperErr != nil || lowerErr != nil || !bytes.Equal(upperCap, lowerCap) {
			return fmt.Errorf("directory capability %q needs walking differ: %w", relative, errdefs.ErrNotImplemented)
		}
		return nil
	})
}

func directoryCapability(path string) ([]byte, error) {
	value, err := sysx.LGetxattr(path, "security.capability")
	if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.ENOTSUP) {
		return nil, nil
	}
	return value, err
}

// contextWriter propagates cancellation through the archive writer, including
// while writing a large file. ChangeWriter's internal file copier otherwise
// uses context.TODO() and cannot observe cancellation during a file copy.
type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w contextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.writer.Write(p)
}

func withCancellation(ctx context.Context, handle fs.ChangeFunc) fs.ChangeFunc {
	return func(kind fs.ChangeKind, path string, info os.FileInfo, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return handle(kind, path, info, err)
	}
}

// writeDiff writes the layer diff from an overlay upper directory to w.
// A safety preflight and fs.DiffDirChanges each walk the upperdir, but neither
// traverses the entire lower tree. The bounded extra upperdir walk avoids
// archiving filesystem changes that continuity cannot safely represent.
func writeDiff(ctx context.Context, w io.Writer, lower []mount.Mount, upperDir string, opts []archive.ChangeWriterOpt) error {
	opaque, err := hasOverlayRootOpacity(upperDir)
	if err != nil {
		return fmt.Errorf("cannot inspect overlay upperdir root opacity: %v: %w", err, errdefs.ErrNotImplemented)
	}
	if opaque {
		return fmt.Errorf("opaque overlay upperdir root requires walking differ: %w", errdefs.ErrNotImplemented)
	}
	return mount.WithTempMount(ctx, lower, func(lowerRoot string) error {
		if err := requireFlatLowerMount(lowerRoot); err != nil {
			return err
		}
		if err := requireSafeUpperDir(ctx, lowerRoot, upperDir); err != nil {
			return err
		}
		cw := archive.NewChangeWriter(contextWriter{ctx: ctx, writer: w}, upperDir, opts...)
		err := fs.DiffDirChanges(ctx, lowerRoot, upperDir, fs.DiffSourceOverlayFS,
			withCancellation(ctx, withOverlayWhiteoutFallback(upperDir, cw.HandleChange)))
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
	if upperDir == "" || !overlayFastPathFeaturesDisabled(upper[0].Options) || !overlayLowerMatches(lower, upper) {
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
