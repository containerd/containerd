package windowsdiffer

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/go-winio/archive/tar"
	"github.com/Microsoft/go-winio/backuptar"
	"github.com/Microsoft/hcsshim"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/diff"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/plugin"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.DiffPlugin,
		ID:   "windows",
		Requires: []plugin.Type{
			plugin.ContentPlugin,
			plugin.MetadataPlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			md, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			return NewWindowsDiff(md.(*metadata.DB).ContentStore())
		},
	})
}

type windowsDiff struct {
	store content.Store
}

var (
	emptyDesc = ocispec.Descriptor{}

	// mutatedFiles is a list of files that are mutated by the import process
	// and must be backed up and restored.
	mutatedFiles = map[string]string{
		"UtilityVM/Files/EFI/Microsoft/Boot/BCD":      "bcd.bak",
		"UtilityVM/Files/EFI/Microsoft/Boot/BCD.LOG":  "bcd.log.bak",
		"UtilityVM/Files/EFI/Microsoft/Boot/BCD.LOG1": "bcd.log1.bak",
		"UtilityVM/Files/EFI/Microsoft/Boot/BCD.LOG2": "bcd.log2.bak",
	}
)

// NewWindowsDiff is the Windows container implementation of diff.Differ.
func NewWindowsDiff(store content.Store) (diff.Differ, error) {
	return &windowsDiff{
		store: store,
	}, nil
}

// Apply applies the content associated with the provided digests onto the
// provided mounts. Archive content will be extracted and decompressed if
// necessary.
func (s *windowsDiff) Apply(ctx context.Context, desc ocispec.Descriptor, mounts []mount.Mount) (ocispec.Descriptor, error) {
	var isCompressed bool
	switch desc.MediaType {
	case ocispec.MediaTypeImageLayer, images.MediaTypeDockerSchema2Layer:
	case ocispec.MediaTypeImageLayerGzip, images.MediaTypeDockerSchema2LayerGzip:
		isCompressed = true
	default:
		// Still apply all generic media types *.tar[.+]gzip and *.tar
		if strings.HasSuffix(desc.MediaType, ".tar.gzip") || strings.HasSuffix(desc.MediaType, ".tar+gzip") {
			isCompressed = true
		} else if !strings.HasSuffix(desc.MediaType, ".tar") {
			return emptyDesc, errors.Wrapf(errdefs.ErrNotImplemented, "unsupported diff media type: %v", desc.MediaType)
		}
	}

	dir, err := ioutil.TempDir("", "extract-")
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to create temporary directory")
	}
	defer os.RemoveAll(dir)

	ra, err := s.store.ReaderAt(ctx, desc.Digest)
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to get reader from content store")
	}
	defer ra.Close()

	r := content.NewReader(ra)
	if isCompressed {
		ds, err := compression.DecompressStream(r)
		if err != nil {
			return emptyDesc, err
		}
		defer ds.Close()
		r = ds
	}

	digester := digest.Canonical.Digester()
	rc := &readCounter{
		r: io.TeeReader(r, digester.Hash()),
	}

	layer, parentLayerPaths, err := mountsToLayerAndParents(mounts)
	if err != nil {
		return emptyDesc, err
	}

	size, err := writeLayer(rc, filepath.Dir(mounts[0].Source), layer, parentLayerPaths...)
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to write layer")
	}

	// TODO (darrenstahlmsft) use archiver instead of writelayer here. Need parent layer context
	//if _, err := archive.Apply(ctx, dir, rc); err != nil {
	//	return emptyDesc, err
	//}

	// Read any trailing data
	if _, err := io.Copy(ioutil.Discard, rc); err != nil {
		return emptyDesc, err
	}

	return ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Size:      size,
		Digest:    digester.Digest(),
	}, nil
}

// writeLayer writes a layer from a tar file.
func writeLayer(layerData io.Reader, home string, layer string, parentLayerPaths ...string) (int64, error) {
	// TODO (darrenstahlmsft): Make this a re-exec to prevent escalating privilege in main containerd.exe process
	err := winio.EnableProcessPrivileges([]string{winio.SeBackupPrivilege, winio.SeRestorePrivilege})
	if err != nil {
		return 0, err
	}
	defer func() {
		if err := winio.DisableProcessPrivileges([]string{winio.SeBackupPrivilege, winio.SeRestorePrivilege}); err != nil {
			// This should never happen, but just in case when in debugging mode.
			// See https://github.com/docker/docker/pull/28002#discussion_r86259241 for rationale.
			panic("Failed to disable process privileges while in non re-exec mode")
		}
	}()

	info := hcsshim.DriverInfo{
		Flavour: 1,
		HomeDir: home,
	}

	w, err := hcsshim.NewLayerWriter(info, filepath.Base(layer), parentLayerPaths)
	if err != nil {
		return 0, err
	}

	size, err := writeLayerFromTar(layerData, w, layer)
	if err != nil {
		return 0, err
	}

	err = w.Close()
	if err != nil {
		return 0, err
	}

	return size, nil
}

func writeLayerFromTar(r io.Reader, w hcsshim.LayerWriter, root string) (int64, error) {
	t := tar.NewReader(r)
	hdr, err := t.Next()
	totalSize := int64(0)
	buf := bufio.NewWriter(nil)
	for err == nil {
		base := path.Base(hdr.Name)
		if strings.HasPrefix(base, whiteoutPrefix) {
			name := path.Join(path.Dir(hdr.Name), base[len(whiteoutPrefix):])
			err = w.Remove(filepath.FromSlash(name))
			if err != nil {
				return 0, err
			}
			hdr, err = t.Next()
		} else if hdr.Typeflag == tar.TypeLink {
			err = w.AddLink(filepath.FromSlash(hdr.Name), filepath.FromSlash(hdr.Linkname))
			if err != nil {
				return 0, err
			}
			hdr, err = t.Next()
		} else {
			var (
				name     string
				size     int64
				fileInfo *winio.FileBasicInfo
			)
			name, size, fileInfo, err = backuptar.FileInfoFromHeader(hdr)
			if err != nil {
				return 0, err
			}
			err = w.Add(filepath.FromSlash(name), fileInfo)
			if err != nil {
				return 0, err
			}
			hdr, err = tarToBackupStreamWithMutatedFiles(buf, w, t, hdr, root)
			totalSize += size
		}
	}
	if err != io.EOF {
		return 0, err
	}
	return totalSize, nil
}

// tarToBackupStreamWithMutatedFiles reads data from a tar stream and
// writes it to a backup stream, and also saves any files that will be mutated
// by the import layer process to a backup location.
func tarToBackupStreamWithMutatedFiles(buf *bufio.Writer, w io.Writer, t *tar.Reader, hdr *tar.Header, root string) (nextHdr *tar.Header, err error) {
	var (
		bcdBackup       *os.File
		bcdBackupWriter *winio.BackupFileWriter
	)
	if backupPath, ok := mutatedFiles[hdr.Name]; ok {
		bcdBackup, err = os.Create(filepath.Join(root, backupPath))
		if err != nil {
			return nil, err
		}
		defer func() {
			cerr := bcdBackup.Close()
			if err == nil {
				err = cerr
			}
		}()

		bcdBackupWriter = winio.NewBackupFileWriter(bcdBackup, false)
		defer func() {
			cerr := bcdBackupWriter.Close()
			if err == nil {
				err = cerr
			}
		}()

		buf.Reset(io.MultiWriter(w, bcdBackupWriter))
	} else {
		buf.Reset(w)
	}

	defer func() {
		ferr := buf.Flush()
		if err == nil {
			err = ferr
		}
	}()

	return backuptar.WriteBackupStreamFromTarFile(buf, t, hdr)
}

// DiffMounts creates a diff between the given mounts and uploads the result
// to the content store.
func (s *windowsDiff) DiffMounts(ctx context.Context, lower, upper []mount.Mount, opts ...diff.Opt) (ocispec.Descriptor, error) {

	var config diff.Config
	for _, opt := range opts {
		if err := opt(&config); err != nil {
			return emptyDesc, err
		}
	}

	if config.MediaType == "" {
		config.MediaType = ocispec.MediaTypeImageLayerGzip
	}

	var isCompressed bool
	switch config.MediaType {
	case ocispec.MediaTypeImageLayer:
	case ocispec.MediaTypeImageLayerGzip:
		isCompressed = true
	default:
		return emptyDesc, errors.Wrapf(errdefs.ErrNotImplemented, "unsupported diff media type: %v", config.MediaType)
	}

	var newReference bool
	if config.Reference == "" {
		newReference = true
		config.Reference = uniqueRef()
	}

	cw, err := s.store.Writer(ctx, config.Reference, 0, "")
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to open writer")
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
			return emptyDesc, err
		}
	}

	layer, parentLayerPaths, err := mountsToLayerAndParents(upper)
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to get layer and parent paths from mounts")
	}
	if lower[0].Source != parentLayerPaths[0] {
		return emptyDesc, errors.Wrapf(errdefs.ErrInvalidArgument, "lower mounts must be the direct child of the upper mounts %v != %v", lower[0].Source, parentLayerPaths[0])
	}

	if isCompressed {
		dgstr := digest.SHA256.Digester()
		compressed, err := compression.CompressStream(cw, compression.Gzip)
		if err != nil {
			return emptyDesc, errors.Wrap(err, "failed to get compressed stream")
		}
		err = s.writeDiff(ctx, io.MultiWriter(compressed, dgstr.Hash()), layer, parentLayerPaths)
		compressed.Close()
		if err != nil {
			return emptyDesc, errors.Wrap(err, "failed to write compressed diff")
		}

		if config.Labels == nil {
			config.Labels = map[string]string{}
		}
		config.Labels["containerd.io/uncompressed"] = dgstr.Digest().String()
	} else {
		if err = s.writeDiff(ctx, cw, layer, parentLayerPaths); err != nil {
			return emptyDesc, errors.Wrap(err, "failed to write diff")
		}
	}

	var commitopts []content.Opt
	if config.Labels != nil {
		commitopts = append(commitopts, content.WithLabels(config.Labels))
	}

	dgst := cw.Digest()
	if err := cw.Commit(ctx, 0, dgst, commitopts...); err != nil {
		return emptyDesc, errors.Wrap(err, "failed to commit")
	}

	info, err := s.store.Info(ctx, dgst)
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to get info from content store")
	}

	return ocispec.Descriptor{
		MediaType: config.MediaType,
		Size:      info.Size,
		Digest:    info.Digest,
	}, nil
}

func mountsToLayerAndParents(mounts []mount.Mount) (string, []string, error) {
	if len(mounts) == 0 {
		return "", nil, errors.Wrap(errdefs.ErrInvalidArgument, "number of mounts should not be 0")
	}
	layer := mounts[0].Source

	var parents []string
	for _, mount := range mounts[1:] {
		parents = append(parents, mount.Source)
	}

	return layer, parents, nil
}

// WriteDiff writes a tar stream of the computed difference between the
// provided directories.
//
// Produces a tar using OCI style file markers for deletions. Deleted
// files will be prepended with the prefix ".wh.". This style is
// based off AUFS whiteouts.
// See https://github.com/opencontainers/image-spec/blob/master/layer.md
func (s *windowsDiff) writeDiff(ctx context.Context, w io.Writer, layer string, parentLayerPaths []string) error {
	info := hcsshim.DriverInfo{
		Flavour: hcsshim.FilterDriver,
		HomeDir: filepath.Dir(layer),
	}
	id := filepath.Base(layer)
	err := winio.RunWithPrivilege(winio.SeBackupPrivilege, func() error {
		r, err := hcsshim.NewLayerReader(info, id, parentLayerPaths)
		if err != nil {
			return err
		}

		err = writeTarFromLayer(r, w)
		cerr := r.Close()
		if err == nil {
			err = cerr
		}
		return err
	})
	return err
}

const (
	// whiteoutPrefix prefix means file is a whiteout. If this is followed by a
	// filename this means that file has been removed from the base layer.
	// See https://github.com/opencontainers/image-spec/blob/master/layer.md#whiteouts
	whiteoutPrefix = ".wh."
)

func writeTarFromLayer(r hcsshim.LayerReader, w io.Writer) error {
	t := tar.NewWriter(w)
	for {
		name, size, fileInfo, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if fileInfo == nil {
			// Write a whiteout file.
			hdr := &tar.Header{
				Name: filepath.ToSlash(filepath.Join(filepath.Dir(name), whiteoutPrefix+filepath.Base(name))),
			}
			err := t.WriteHeader(hdr)
			if err != nil {
				return err
			}
		} else {
			err = backuptar.WriteTarFileFromBackupStream(t, r, name, size, fileInfo)
			if err != nil {
				return err
			}
		}
	}
	return t.Close()
}

type readCounter struct {
	r io.Reader
	c int64
}

func (rc *readCounter) Read(p []byte) (n int, err error) {
	n, err = rc.r.Read(p)
	rc.c += int64(n)
	return
}

func uniqueRef() string {
	t := time.Now()
	var b [3]byte
	// Ignore read failures, just decreases uniqueness
	rand.Read(b[:])
	return fmt.Sprintf("%d-%s", t.UnixNano(), base64.URLEncoding.EncodeToString(b[:]))
}
