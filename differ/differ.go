package differ

import (
	"io"
	"io/ioutil"
	"os"
	"strings"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
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
		ID:   "walking",
		Requires: []plugin.PluginType{
			plugin.ContentPlugin,
			plugin.MetadataPlugin,
		},
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			c, err := ic.Get(plugin.ContentPlugin)
			if err != nil {
				return nil, err
			}
			md, err := ic.Get(plugin.MetadataPlugin)
			if err != nil {
				return nil, err
			}
			return NewWalkingDiff(metadata.NewContentStore(md.(*bolt.DB), c.(content.Store)))
		},
	})
}

type walkingDiff struct {
	store content.Store
}

var emptyDesc = ocispec.Descriptor{}

// NewWalkingDiff is a generic implementation of plugin.Differ.
// NewWalkingDiff is expected to work with any filesystem.
func NewWalkingDiff(store content.Store) (plugin.Differ, error) {
	return &walkingDiff{
		store: store,
	}, nil
}

// Apply applies the content associated with the provided digests onto the
// provided mounts. Archive content will be extracted and decompressed if
// necessary.
func (s *walkingDiff) Apply(ctx context.Context, desc ocispec.Descriptor, mounts []mount.Mount) (ocispec.Descriptor, error) {
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

	if err := mount.MountAll(mounts, dir); err != nil {
		return emptyDesc, errors.Wrap(err, "failed to mount")
	}
	defer mount.Unmount(dir, 0)

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

	if _, err := archive.Apply(ctx, dir, rc); err != nil {
		return emptyDesc, err
	}

	// Read any trailing data
	if _, err := io.Copy(ioutil.Discard, rc); err != nil {
		return emptyDesc, err
	}

	return ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Size:      rc.c,
		Digest:    digester.Digest(),
	}, nil
}

// DiffMounts creates a diff between the given mounts and uploads the result
// to the content store.
func (s *walkingDiff) DiffMounts(ctx context.Context, lower, upper []mount.Mount, media, ref string) (ocispec.Descriptor, error) {
	var isCompressed bool
	switch media {
	case ocispec.MediaTypeImageLayer:
	case ocispec.MediaTypeImageLayerGzip:
		isCompressed = true
	case "":
		media = ocispec.MediaTypeImageLayerGzip
		isCompressed = true
	default:
		return emptyDesc, errors.Wrapf(errdefs.ErrNotImplemented, "unsupported diff media type: %v", media)
	}
	aDir, err := ioutil.TempDir("", "left-")
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to create temporary directory")
	}
	defer os.RemoveAll(aDir)

	bDir, err := ioutil.TempDir("", "right-")
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to create temporary directory")
	}
	defer os.RemoveAll(bDir)

	if err := mount.MountAll(lower, aDir); err != nil {
		return emptyDesc, errors.Wrap(err, "failed to mount")
	}
	defer mount.Unmount(aDir, 0)

	if err := mount.MountAll(upper, bDir); err != nil {
		return emptyDesc, errors.Wrap(err, "failed to mount")
	}
	defer mount.Unmount(bDir, 0)

	cw, err := s.store.Writer(ctx, ref, 0, "")
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to open writer")
	}

	var opts []content.Opt
	if isCompressed {
		dgstr := digest.SHA256.Digester()
		compressed, err := compression.CompressStream(cw, compression.Gzip)
		if err != nil {
			return emptyDesc, errors.Wrap(err, "failed to get compressed stream")
		}
		err = archive.WriteDiff(ctx, io.MultiWriter(compressed, dgstr.Hash()), aDir, bDir)
		compressed.Close()
		if err != nil {
			return emptyDesc, errors.Wrap(err, "failed to write compressed diff")
		}
		opts = append(opts, content.WithLabels(map[string]string{
			"containerd.io/uncompressed": dgstr.Digest().String(),
		}))
	} else {
		if err = archive.WriteDiff(ctx, cw, aDir, bDir); err != nil {
			return emptyDesc, errors.Wrap(err, "failed to write diff")
		}
	}

	dgst := cw.Digest()
	if err := cw.Commit(ctx, 0, dgst, opts...); err != nil {
		return emptyDesc, errors.Wrap(err, "failed to commit")
	}

	info, err := s.store.Info(ctx, dgst)
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to get info from content store")
	}

	return ocispec.Descriptor{
		MediaType: media,
		Size:      info.Size,
		Digest:    info.Digest,
	}, nil
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
