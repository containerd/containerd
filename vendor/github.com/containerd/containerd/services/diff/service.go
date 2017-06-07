package diff

import (
	"io"
	"io/ioutil"
	"os"

	diffapi "github.com/containerd/containerd/api/services/diff"
	"github.com/containerd/containerd/api/types/descriptor"
	mounttypes "github.com/containerd/containerd/api/types/mount"
	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/snapshot"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register("diff-grpc", &plugin.Registration{
		Type: plugin.GRPCPlugin,
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			return newService(ic.Content, ic.Snapshotter)
		},
	})
}

type service struct {
	store       content.Store
	snapshotter snapshot.Snapshotter
}

func newService(store content.Store, snapshotter snapshot.Snapshotter) (*service, error) {
	return &service{
		store:       store,
		snapshotter: snapshotter,
	}, nil
}

func (s *service) Register(gs *grpc.Server) error {
	diffapi.RegisterDiffServer(gs, s)
	return nil
}

func (s *service) Apply(ctx context.Context, er *diffapi.ApplyRequest) (*diffapi.ApplyResponse, error) {
	desc := toDescriptor(er.Diff)
	// TODO: Check for supported media types

	mounts := toMounts(er.Mounts)

	dir, err := ioutil.TempDir("", "extract-")
	if err != nil {
		return nil, errors.Wrap(err, "failed to create temporary directory")
	}
	defer os.RemoveAll(dir)

	if err := mount.MountAll(mounts, dir); err != nil {
		return nil, errors.Wrap(err, "failed to mount")
	}
	defer mount.Unmount(dir, 0)

	r, err := s.store.Reader(ctx, desc.Digest)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get reader from content store")
	}
	defer r.Close()

	// TODO: only decompress stream if media type is compressed
	ds, err := compression.DecompressStream(r)
	if err != nil {
		return nil, err
	}
	defer ds.Close()

	digester := digest.Canonical.Digester()
	rc := &readCounter{
		r: io.TeeReader(ds, digester.Hash()),
	}

	if _, err := archive.Apply(ctx, dir, rc); err != nil {
		return nil, err
	}

	// Read any trailing data
	if _, err := io.Copy(ioutil.Discard, rc); err != nil {
		return nil, err
	}

	resp := &diffapi.ApplyResponse{
		Applied: &descriptor.Descriptor{
			MediaType: ocispec.MediaTypeImageLayer,
			Digest:    digester.Digest(),
			Size_:     rc.c,
		},
	}

	return resp, nil
}

func (s *service) Diff(ctx context.Context, dr *diffapi.DiffRequest) (*diffapi.DiffResponse, error) {
	aMounts := toMounts(dr.Left)
	bMounts := toMounts(dr.Right)

	aDir, err := ioutil.TempDir("", "left-")
	if err != nil {
		return nil, errors.Wrap(err, "failed to create temporary directory")
	}
	defer os.RemoveAll(aDir)

	bDir, err := ioutil.TempDir("", "right-")
	if err != nil {
		return nil, errors.Wrap(err, "failed to create temporary directory")
	}
	defer os.RemoveAll(bDir)

	if err := mount.MountAll(aMounts, aDir); err != nil {
		return nil, errors.Wrap(err, "failed to mount")
	}
	defer mount.Unmount(aDir, 0)

	if err := mount.MountAll(bMounts, bDir); err != nil {
		return nil, errors.Wrap(err, "failed to mount")
	}
	defer mount.Unmount(bDir, 0)

	cw, err := s.store.Writer(ctx, dr.Ref, 0, "")
	if err != nil {
		return nil, errors.Wrap(err, "failed to open writer")
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
		return nil, errors.Wrap(err, "failed to write diff")
	}

	dgst := cw.Digest()
	if err := cw.Commit(0, dgst); err != nil {
		return nil, errors.Wrap(err, "failed to commit")
	}

	info, err := s.store.Info(ctx, dgst)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get info from content store")
	}

	desc := ocispec.Descriptor{
		MediaType: dr.MediaType,
		Digest:    info.Digest,
		Size:      info.Size,
	}

	return &diffapi.DiffResponse{
		Diff: fromDescriptor(desc),
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

func toDescriptor(d *descriptor.Descriptor) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: d.MediaType,
		Digest:    d.Digest,
		Size:      d.Size_,
	}
}

func toMounts(apim []*mounttypes.Mount) []mount.Mount {
	mounts := make([]mount.Mount, len(apim))
	for i, m := range apim {
		mounts[i] = mount.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		}
	}
	return mounts
}
