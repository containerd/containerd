package diff

import (
	diffapi "github.com/containerd/containerd/api/services/diff"
	mounttypes "github.com/containerd/containerd/api/types/mount"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/plugin"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register("diff-grpc", &plugin.Registration{
		Type: plugin.GRPCPlugin,
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			return &service{
				diff: ic.Differ,
			}, nil
		},
	})
}

type service struct {
	diff plugin.Differ
}

func (s *service) Register(gs *grpc.Server) error {
	diffapi.RegisterDiffServer(gs, s)
	return nil
}

func (s *service) Apply(ctx context.Context, er *diffapi.ApplyRequest) (*diffapi.ApplyResponse, error) {
	desc := toDescriptor(er.Diff)
	// TODO: Check for supported media types

	mounts := toMounts(er.Mounts)

	ocidesc, err := s.diff.Apply(ctx, desc, mounts)
	if err != nil {
		return nil, err
	}

	return &diffapi.ApplyResponse{
		Applied: fromDescriptor(ocidesc),
	}, nil

}

func (s *service) Diff(ctx context.Context, dr *diffapi.DiffRequest) (*diffapi.DiffResponse, error) {
	aMounts := toMounts(dr.Left)
	bMounts := toMounts(dr.Right)

	ocidesc, err := s.diff.DiffMounts(ctx, aMounts, bMounts, dr.MediaType, dr.Ref)
	if err != nil {
		return nil, err
	}

	return &diffapi.DiffResponse{
		Diff: fromDescriptor(ocidesc),
	}, nil
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
