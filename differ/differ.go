package differ

import (
	"io"
	"io/ioutil"
	"os"

	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/content"
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
		ID:   "base-diff",
		Requires: []plugin.PluginType{
			plugin.ContentPlugin,
		},
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			c, err := ic.Get(plugin.ContentPlugin)
			if err != nil {
				return nil, err
			}
			return NewBaseDiff(c.(content.Store))
		},
	})
}

type BaseDiff struct {
	store content.Store
}

var _ plugin.Differ = &BaseDiff{}

var emptyDesc = ocispec.Descriptor{}

func NewBaseDiff(store content.Store) (*BaseDiff, error) {
	return &BaseDiff{
		store: store,
	}, nil
}

func (s *BaseDiff) Apply(ctx context.Context, desc ocispec.Descriptor, mounts []mount.Mount) (ocispec.Descriptor, error) {
	// TODO: Check for supported media types
	dir, err := ioutil.TempDir("", "extract-")
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to create temporary directory")
	}
	defer os.RemoveAll(dir)

	if err := mount.MountAll(mounts, dir); err != nil {
		return emptyDesc, errors.Wrap(err, "failed to mount")
	}
	defer mount.Unmount(dir, 0)

	r, err := s.store.Reader(ctx, desc.Digest)
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to get reader from content store")
	}
	defer r.Close()

	// TODO: only decompress stream if media type is compressed
	ds, err := compression.DecompressStream(r)
	if err != nil {
		return emptyDesc, err
	}
	defer ds.Close()

	digester := digest.Canonical.Digester()
	rc := &readCounter{
		r: io.TeeReader(ds, digester.Hash()),
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

func (s *BaseDiff) DiffMounts(ctx context.Context, lower, upper []mount.Mount, media, ref string) (ocispec.Descriptor, error) {

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

	// TODO: Validate media type

	// TODO: Support compressed media types (link compressed to uncompressed)
	//dgstr := digest.SHA256.Digester()
	//wc := &writeCounter{}
	//compressed, err := compression.CompressStream(cw, compression.Gzip)
	//if err != nil {
	//	return nil, errors.Wrap(err, "failed to get compressed stream")
	//}
	//err = archive.WriteDiff(ctx, io.MultiWriter(compressed, dgstr.Hash(), wc), lowerDir, upperDir)
	//compressed.Close()

	err = archive.WriteDiff(ctx, cw, aDir, bDir)
	if err != nil {
		return emptyDesc, errors.Wrap(err, "failed to write diff")
	}

	dgst := cw.Digest()
	if err := cw.Commit(0, dgst); err != nil {
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
