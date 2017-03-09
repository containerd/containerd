package rootfs

import (
	"syscall"

	"github.com/docker/containerd"
	rootfsapi "github.com/docker/containerd/api/services/rootfs"
	"github.com/docker/containerd/content"
	"github.com/docker/containerd/log"
	"github.com/docker/containerd/plugin"
	"github.com/docker/containerd/rootfs"
	"github.com/docker/containerd/snapshot"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register("rootfs-grpc", &plugin.Registration{
		Type: plugin.GRPCPlugin,
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			return NewService(ic.Store, ic.Snapshotter)
		},
	})
}

type Service struct {
	store       *content.Store
	snapshotter snapshot.Snapshotter
}

func NewService(store *content.Store, snapshotter snapshot.Snapshotter) (*Service, error) {
	return &Service{
		store:       store,
		snapshotter: snapshotter,
	}, nil
}

func (s *Service) Register(gs *grpc.Server) error {
	rootfsapi.RegisterRootFSServer(gs, s)
	return nil
}

func (s *Service) Prepare(ctx context.Context, pr *rootfsapi.PrepareRequest) (*rootfsapi.PrepareResponse, error) {
	layers := make([]ocispec.Descriptor, len(pr.Layers))
	for i, l := range pr.Layers {
		layers[i] = ocispec.Descriptor{
			MediaType: l.MediaType,
			Digest:    l.Digest,
			Size:      l.MediaSize,
		}
	}
	log.G(ctx).Infof("Preparing %#v", layers)
	chainID, err := rootfs.Prepare(ctx, s.snapshotter, mounter{}, layers, s.store.Reader, emptyResolver, noopRegister)
	if err != nil {
		log.G(ctx).Errorf("Rootfs Prepare failed!: %v", err)
		return nil, err
	}
	log.G(ctx).Infof("ChainID %#v", chainID)
	return &rootfsapi.PrepareResponse{
		ChainID: chainID,
	}, nil
}

type mounter struct{}

func (mounter) Mount(dir string, mounts ...containerd.Mount) error {
	return containerd.MountAll(mounts, dir)
}

func (mounter) Unmount(dir string) error {
	return syscall.Unmount(dir, 0)
}

func emptyResolver(digest.Digest) digest.Digest {
	return digest.Digest("")
}

func noopRegister(digest.Digest, digest.Digest) error {
	return nil
}

//func (s *Service) Mounts(ctx context.Context, mr *rootfsapi.MountRequest) (*rootfsapi.MountResponse, error) {
//
//}
