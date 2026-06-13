// Package tarconv ingests OCI/Docker tar layer streams into an [erofs.Writer]
// via direct writer calls, without staging an intermediate fs.FS.
//
// The single entry point is [Apply]. It handles all tar entry types (regular
// files, directories, symlinks, hard links, device nodes, FIFOs) and three
// whiteout strategies selectable via options:
//
//   - Default (no option): translate AUFS/OCI whiteouts to overlayfs xattrs.
//     Suitable for per-layer EROFS images that will be stacked at runtime.
//   - [WithMerge]: resolve whiteouts structurally by removing entries.
//     Suitable for flat merged images where all layers are applied in sequence.
//   - [WithPreserveWhiteouts]: keep .wh.* entries as plain files.
//     Suitable for tooling that needs the raw tar content.
//
// Tar-index mode is enabled by creating the [erofs.Writer] with
// [erofs.WithDataFile] pointing at a file that receives the raw tar bytes,
// and passing that same file to Apply via [WithTarIndexData].  In this mode
// Apply records file-data ranges in the EROFS image as chunk-index entries
// that reference the external data file rather than copying bytes into the
// EROFS spool.  The result is a compact metadata-only EROFS image whose
// chunk table points into the appended original tar content.
package tarconv

import (
	archivetar "archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	erofs "github.com/erofs/go-erofs"
)

// Unix inode type bits (S_IF*), matching the values expected by erofs.Writer.Mknod.
const (
	sifChrdev = uint16(0020000) // character device
	sifBlkdev = uint16(0060000) // block device
	sifFifo   = uint16(0010000) // FIFO / named pipe
)

const (
	whiteoutPrefix     = ".wh."
	opaqueWhiteout     = ".wh..wh..opq"
	overlayOpaqueXattr = "trusted.overlay.opaque"
	overlayOriginXattr = "trusted.overlay.origin"
	xattrPrefix        = "SCHILY.xattr."
)

// whiteoutMode selects how AUFS/OCI whiteout entries are processed.
type whiteoutMode int

const (
	// whiteoutConvert is the default: translate whiteouts to overlayfs representation.
	whiteoutConvert whiteoutMode = iota
	// whiteoutMerge resolves whiteouts by removing entries from the writer tree.
	whiteoutMerge
	// whiteoutPreserve keeps whiteout entries as plain regular files.
	whiteoutPreserve
)

// config holds the parsed options for an Apply call.
type config struct {
	whiteouts    whiteoutMode
	tarIndexData *os.File // non-nil: tar-index mode — data written here, not into EROFS spool
}

// Option configures an [Apply] call.
type Option func(*config)

// WithMerge makes Apply resolve AUFS/OCI whiteout entries structurally:
//   - .wh.<name> removes the sibling path from the writer's current tree.
//     ErrNotExist is silently swallowed (the target may not yet exist in any
//     layer seen so far).
//   - .wh..wh..opq removes all existing children of the containing directory,
//     leaving the directory itself so subsequent entries can repopulate it.
//
// The resulting image is a flat merged filesystem with no overlay xattrs.
// Use WithMerge when calling Apply once per layer to build a single merged image.
func WithMerge() Option {
	return func(c *config) { c.whiteouts = whiteoutMerge }
}

// WithPreserveWhiteouts makes Apply treat .wh.* and .wh..wh..opq entries as
// ordinary regular files, performing no whiteout translation. The raw tar
// content is preserved verbatim.
func WithPreserveWhiteouts() Option {
	return func(c *config) { c.whiteouts = whiteoutPreserve }
}

// WithTarIndexData enables tar-index mode.
//
// In tar-index mode Apply does not copy file payload bytes into the EROFS
// writer's internal spool.  Instead, each regular file's data is written to
// dataFile at a 512-byte-aligned offset that matches the file's position in
// the tar stream, and the corresponding EROFS inode is stored as a
// chunk-index entry referencing dataFile.
//
// The caller must create the [erofs.Writer] with [erofs.WithDataFile](dataFile)
// using the same *os.File so that go-erofs can record the correct chunk
// DeviceID.  The resulting EROFS image contains only filesystem metadata and
// chunk indexes; raw tar content is appended to dataFile by the caller after
// Apply returns to produce the final combined blob.
//
// Block size: the Writer's block size must be 512 (tar's natural granularity).
// Use [erofs.WithBlockSize](512) when calling [erofs.Create].
func WithTarIndexData(dataFile *os.File) Option {
	return func(c *config) { c.tarIndexData = dataFile }
}

// pendingLink records a hard link whose target had not yet appeared when the
// link entry was processed. Only header metadata is stored; no payload bytes.
type pendingLink struct {
	newname string
	oldname string // target path, cleaned
	hdr     archivetar.Header
}

// Apply ingests the tar stream r into w, translating each entry into a direct
// [erofs.Writer] call.
//
// By default (no options), AUFS/OCI whiteout entries are translated to
// overlayfs-compatible representation:
//   - .wh.<name> becomes a character device 0/0 at the sibling path, and the
//     containing directory receives trusted.overlay.origin="".
//   - .wh..wh..opq sets trusted.overlay.opaque=y on the containing directory.
//
// This matches the behaviour of mkfs.erofs --aufs and is appropriate for
// single-layer EROFS images that will be stacked by an overlayfs consumer.
//
// Use [WithMerge] to resolve whiteouts structurally instead (flat merged image).
// Use [WithPreserveWhiteouts] to keep whiteout entries as plain files.
//
// Hard links may appear in any order. Links whose targets have not yet appeared
// are queued and resolved as subsequent entries are processed. An unresolved
// hard link at EOF is returned as an error.
func Apply(w *erofs.Writer, r io.Reader, opts ...Option) error {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}

	// In tar-index mode we wrap r with a counter so we can determine
	// each file's data start offset within the tar stream.
	var cr *countingReader
	if cfg.tarIndexData != nil {
		cr = &countingReader{r: r}
		r = cr
	}

	tr := archivetar.NewReader(r)

	// pending records hard links whose targets haven't appeared yet.
	var pending []pendingLink

	// pendingOrigin records directories that need trusted.overlay.origin=""
	// set once their TypeDir entry appears (handles whiteout-before-dir order).
	var pendingOrigin map[string]bool

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tarconv: %w", err)
		}

		p := cleanTarPath(hdr.Name)
		base := path.Base(p)
		dir := path.Dir(p)

		// --- Whiteout detection ---
		// OCI whiteouts use TypeReg. Detect by name prefix and dispatch
		// before the normal type switch so they are never added as real entries
		// (unless WithPreserveWhiteouts is active).
		if cfg.whiteouts != whiteoutPreserve && strings.HasPrefix(base, whiteoutPrefix) {
			if base == opaqueWhiteout {
				switch cfg.whiteouts {
				case whiteoutMerge:
					if err := removeChildren(w, dir); err != nil {
						return fmt.Errorf("tarconv: opaque %s: %w", dir, err)
					}
				default: // whiteoutConvert
					if err := setOpaqueXattr(w, dir, hdr); err != nil {
						return fmt.Errorf("tarconv: opaque %s: %w", dir, err)
					}
				}
			} else {
				target := path.Join(dir, base[len(whiteoutPrefix):])
				switch cfg.whiteouts {
				case whiteoutMerge:
					if err := w.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
						return fmt.Errorf("tarconv: whiteout %s: %w", target, err)
					}
				default: // whiteoutConvert
					if err := emitWhiteout(w, target, hdr); err != nil {
						return fmt.Errorf("tarconv: whiteout %s: %w", target, err)
					}
					// Set trusted.overlay.origin="" on the parent directory to
					// match mkfs.erofs --aufs behaviour for regular whiteouts.
					if _, serr := w.Stat(dir); serr == nil {
						if err := w.Setxattr(dir, overlayOriginXattr, ""); err != nil {
							return fmt.Errorf("tarconv: whiteout origin %s: %w", dir, err)
						}
					} else {
						// Dir not yet seen — queue for when it appears.
						if pendingOrigin == nil {
							pendingOrigin = make(map[string]bool)
						}
						pendingOrigin[dir] = true
					}
				}
			}
			// Drain any data bytes (whiteouts are zero-size in practice but be safe).
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return fmt.Errorf("tarconv: drain %s: %w", p, err)
			}
			continue
		}

		// --- Normal entry dispatch ---
		switch hdr.Typeflag {
		case archivetar.TypeDir:
			if err := addDir(w, p, hdr); err != nil {
				return fmt.Errorf("tarconv: %s: %w", p, err)
			}
			if pendingOrigin[p] {
				if err := w.Setxattr(p, overlayOriginXattr, ""); err != nil {
					return fmt.Errorf("tarconv: whiteout origin %s: %w", p, err)
				}
				delete(pendingOrigin, p)
			}

		case archivetar.TypeReg, archivetar.TypeRegA: //nolint:staticcheck
			// Remove any existing entry to handle tar overwrite semantics.
			removeExisting(w, p)
			if cfg.tarIndexData != nil {
				// Tar-index mode: record chunk indexes into the data file.
				// cr.n is the byte position *after* the tar header; that is
				// exactly where this file's data starts in the stream.
				dataOffset := cr.n
				if err := addFileTarIndex(w, p, hdr, tr, dataOffset); err != nil {
					return fmt.Errorf("tarconv: %s: %w", p, err)
				}
			} else {
				if err := addFile(w, p, hdr, tr); err != nil {
					return fmt.Errorf("tarconv: %s: %w", p, err)
				}
			}
			pending = replayPending(w, pending)

		case archivetar.TypeSymlink:
			removeExisting(w, p)
			if err := addSymlink(w, p, hdr); err != nil {
				return fmt.Errorf("tarconv: %s: %w", p, err)
			}
			pending = replayPending(w, pending)

		case archivetar.TypeLink:
			oldname := cleanTarPath(hdr.Linkname)
			err := w.Link(oldname, p)
			if err == nil {
				if err := applyMetadata(w, p, hdr); err != nil {
					return fmt.Errorf("tarconv: %s metadata: %w", p, err)
				}
				pending = replayPending(w, pending)
			} else if isNotExist(err) {
				pending = append(pending, pendingLink{newname: p, oldname: oldname, hdr: *hdr})
			} else {
				return fmt.Errorf("tarconv: hardlink %s→%s: %w", p, oldname, err)
			}

		case archivetar.TypeChar, archivetar.TypeBlock:
			removeExisting(w, p)
			if err := addDevice(w, p, hdr); err != nil {
				return fmt.Errorf("tarconv: %s: %w", p, err)
			}
			pending = replayPending(w, pending)

		case archivetar.TypeFifo:
			removeExisting(w, p)
			if err := addFifo(w, p, hdr); err != nil {
				return fmt.Errorf("tarconv: %s: %w", p, err)
			}
			pending = replayPending(w, pending)

		case archivetar.TypeXGlobalHeader:
			// archive/tar merges PAX global headers into subsequent entries automatically.

		default:
			// Skip unrecognised entry types so future tar extensions don't break consumers.
		}
	}

	// Drain the remainder of the underlying stream to EOF. Tar archives have
	// end-of-archive padding (two 512-byte zero blocks) and callers may wrap r
	// in a pipe or network stream that requires the reader side to be fully
	// consumed before the writer side can detect a clean close.
	_, _ = io.Copy(io.Discard, r)

	if len(pending) > 0 {
		return fmt.Errorf("tarconv: unresolved hard link %q → %q (target never appeared)",
			pending[0].newname, pending[0].oldname)
	}
	return nil
}

// --- Entry creation helpers ---

func addDir(w *erofs.Writer, p string, hdr *archivetar.Header) error {
	if err := w.Mkdir(p, tarModeToGoMode(hdr.Mode)); err != nil {
		// Tar archives commonly emit directory entries multiple times (once
		// implicitly when a child is created, once explicitly with metadata).
		// If the path already exists as a directory treat it as a metadata update.
		if isDuplicatePath(err) {
			if info, serr := w.Stat(p); serr == nil && info.IsDir() {
				return applyMetadata(w, p, hdr)
			}
		}
		return err
	}
	return applyMetadata(w, p, hdr)
}

func addFile(w *erofs.Writer, p string, hdr *archivetar.Header, tr *archivetar.Reader) error {
	f, err := w.Create(p)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, tr); err != nil {
		_ = f.Close()
		return fmt.Errorf("copy data: %w", err)
	}
	if err := f.Chmod(tarModeToGoMode(hdr.Mode)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chown(hdr.Uid, hdr.Gid); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return applyMetadata(w, p, hdr)
}

func addSymlink(w *erofs.Writer, p string, hdr *archivetar.Header) error {
	if err := w.Symlink(hdr.Linkname, p); err != nil {
		return err
	}
	return applyMetadata(w, p, hdr)
}

func addDevice(w *erofs.Writer, p string, hdr *archivetar.Header) error {
	typeBits := sifBlkdev
	if hdr.Typeflag == archivetar.TypeChar {
		typeBits = sifChrdev
	}
	mode := typeBits | uint16(tarModeToGoMode(hdr.Mode).Perm())
	if err := w.Mknod(p, mode, mkdev(hdr.Devmajor, hdr.Devminor)); err != nil {
		return err
	}
	return applyMetadata(w, p, hdr)
}

func addFifo(w *erofs.Writer, p string, hdr *archivetar.Header) error {
	mode := sifFifo | uint16(tarModeToGoMode(hdr.Mode).Perm())
	if err := w.Mknod(p, mode, 0); err != nil {
		return err
	}
	return applyMetadata(w, p, hdr)
}

// emitWhiteout creates an overlayfs whiteout device (char 0:0, mode 0) at
// target, used by the default whiteout convert mode.
func emitWhiteout(w *erofs.Writer, target string, hdr *archivetar.Header) error {
	removeExisting(w, target)
	if err := w.Mknod(target, sifChrdev, 0); err != nil {
		return err
	}
	return w.Chtimes(target, hdr.ModTime, hdr.ModTime)
}

// setOpaqueXattr sets trusted.overlay.opaque=y on dir, used by the default
// whiteout convert mode for .wh..wh..opq entries. If the directory does not
// yet exist a placeholder is created; a later TypeDir entry will update it.
func setOpaqueXattr(w *erofs.Writer, dir string, hdr *archivetar.Header) error {
	if _, err := w.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := w.Mkdir(dir, 0o755); err != nil {
			return err
		}
		_ = w.Chtimes(dir, hdr.ModTime, hdr.ModTime)
	}
	return w.Setxattr(dir, overlayOpaqueXattr, "y")
}

// removeChildren removes all direct and indirect descendants of dir from w.
// The directory itself is kept. Used by WithMerge for opaque directories.
func removeChildren(w *erofs.Writer, dir string) error {
	f, err := w.Open(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	rdf, ok := f.(fs.ReadDirFile)
	if !ok {
		return nil
	}
	children, err := rdf.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, child := range children {
		childPath := path.Join(dir, child.Name())
		if child.IsDir() {
			if err := removeAll(w, childPath); err != nil {
				return err
			}
		} else {
			if err := w.Remove(childPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// removeAll recursively removes p and all its descendants from w.
func removeAll(w *erofs.Writer, p string) error {
	info, err := w.Stat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		if err := removeChildren(w, p); err != nil {
			return err
		}
	}
	if err := w.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// removeExisting removes p silently. Used before re-creating a path to handle
// tar overwrite semantics.
func removeExisting(w *erofs.Writer, p string) {
	_ = removeAll(w, p)
}

// applyMetadata applies uid/gid, mtime, full mode (including special bits),
// and xattrs from hdr to path p.
func applyMetadata(w *erofs.Writer, p string, hdr *archivetar.Header) error {
	if err := w.Chown(p, hdr.Uid, hdr.Gid); err != nil {
		return err
	}
	if err := w.Chmod(p, tarModeToGoMode(hdr.Mode)); err != nil {
		return err
	}
	if !hdr.ModTime.IsZero() {
		if err := w.Chtimes(p, hdr.ModTime, hdr.ModTime); err != nil {
			return err
		}
	}
	for k, v := range extractXattrs(hdr) {
		if err := w.Setxattr(p, k, v); err != nil {
			return err
		}
	}
	return nil
}

// extractXattrs returns PAX xattr records from hdr with the SCHILY.xattr. prefix stripped.
func extractXattrs(hdr *archivetar.Header) map[string]string {
	if len(hdr.PAXRecords) == 0 {
		return nil
	}
	var result map[string]string
	for k, v := range hdr.PAXRecords {
		if strings.HasPrefix(k, xattrPrefix) {
			if result == nil {
				result = make(map[string]string)
			}
			result[k[len(xattrPrefix):]] = v
		}
	}
	return result
}

// replayPending tries to resolve queued hard links. Repeats until no progress
// is made to handle chains of pending links.
func replayPending(w *erofs.Writer, pending []pendingLink) []pendingLink {
	for {
		var remaining []pendingLink
		progress := false
		for _, pl := range pending {
			if err := w.Link(pl.oldname, pl.newname); err == nil {
				_ = applyMetadata(w, pl.newname, &pl.hdr)
				progress = true
			} else {
				remaining = append(remaining, pl)
			}
		}
		pending = remaining
		if !progress {
			break
		}
	}
	return pending
}

// isNotExist reports whether err indicates a path does not exist.
func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || strings.Contains(err.Error(), "not found")
}

// isDuplicatePath reports whether err is the "duplicate path" error from erofs.Writer.
func isDuplicatePath(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate path")
}

// tarModeToGoMode converts a tar header Mode (unix mode bits) to fs.FileMode,
// correctly translating the special bits (setuid/setgid/sticky).
func tarModeToGoMode(mode int64) fs.FileMode {
	m := fs.FileMode(mode & 0o777)
	if mode&0o4000 != 0 {
		m |= fs.ModeSetuid
	}
	if mode&0o2000 != 0 {
		m |= fs.ModeSetgid
	}
	if mode&0o1000 != 0 {
		m |= fs.ModeSticky
	}
	return m
}

// cleanTarPath converts a tar header name to a cleaned absolute path.
func cleanTarPath(name string) string {
	if name == "." || name == "" {
		return "/"
	}
	if name[0] != '/' {
		name = "/" + name
	}
	return path.Clean(name)
}

// mkdev constructs a Linux device number from major and minor components.
func mkdev(major, minor int64) uint32 {
	return uint32((major << 8) | (minor & 0xff) | ((minor & ^int64(0xff)) << 12))
}

// addFileTarIndex adds a regular file in tar-index mode.
//
// The file's payload bytes are consumed from tr and discarded (the EROFS
// Writer, created with WithDataFile, will record chunk indexes based on
// dataOffset and hdr.Size).  We create the EROFS File, write the bytes
// through it so that the Writer tracks the data-file position correctly,
// and then apply metadata.
//
// dataOffset is the byte position of this file's data within the underlying
// tar stream (i.e. the value of countingReader.n immediately after the
// archive/tar package has read the header for this entry).
func addFileTarIndex(w *erofs.Writer, p string, hdr *archivetar.Header, tr *archivetar.Reader, _ int64) error {
	// Create the EROFS file entry.  With the Writer in WithDataFile mode,
	// f.Write() forwards data to the external data file and closeDataFile()
	// records the corresponding chunk indexes.
	f, err := w.Create(p)
	if err != nil {
		return err
	}
	// Copy file data: in WithDataFile mode this writes to the external data
	// file and advances the Writer's dataOff counter.
	if _, err := io.Copy(f, tr); err != nil {
		_ = f.Close()
		return fmt.Errorf("copy data (tar-index): %w", err)
	}
	if err := f.Chmod(tarModeToGoMode(hdr.Mode)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chown(hdr.Uid, hdr.Gid); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return applyMetadata(w, p, hdr)
}

// countingReader wraps an io.Reader and counts bytes read.
type countingReader struct {
	r io.Reader
	n int64 // total bytes read so far
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
