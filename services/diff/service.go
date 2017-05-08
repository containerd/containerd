package diff

import (
	"io"
	"io/ioutil"
	"os"

	"github.com/containerd/containerd"
	diffapi "github.com/containerd/containerd/api/services/diff"
	"github.com/containerd/containerd/api/types/descriptor"
	mounttypes "github.com/containerd/containerd/api/types/mount"
	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/content"
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

	if err := containerd.MountAll(mounts, dir); err != nil {
		return nil, errors.Wrap(err, "failed to mount")
	}
	defer containerd.Unmount(dir, 0)

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

func (s *service) Diff(context.Context, *diffapi.DiffRequest) (*diffapi.DiffResponse, error) {
	return nil, errors.New("not implemented")
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

func toMounts(apim []*mounttypes.Mount) []containerd.Mount {
	mounts := make([]containerd.Mount, len(apim))
	for i, m := range apim {
		mounts[i] = containerd.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		}
	}
	return mounts
}
