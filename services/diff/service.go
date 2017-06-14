package diff

import (
	diffapi "github.com/containerd/containerd/api/services/diff"
	mounttypes "github.com/containerd/containerd/api/types/mount"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/snapshot"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "diff",
		Requires: []plugin.PluginType{
			plugin.DiffPlugin,
			plugin.SnapshotPlugin,
		},
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			d, err := ic.Get(plugin.DiffPlugin)
			if err != nil {
				return nil, err
			}
			sn, err := ic.Get(plugin.SnapshotPlugin)
			if err != nil {
				return nil, err
			}
			return &service{
				diff:        d.(plugin.Differ),
				snapshotter: sn.(snapshot.Snapshotter),
			}, nil
		},
	})
}

type service struct {
	diff        plugin.Differ
	snapshotter snapshot.Snapshotter
}

func (s *service) Register(gs *grpc.Server) error {
	diffapi.RegisterDiffServer(gs, s)
	return nil
}

func (s *service) Apply(ctx context.Context, er *diffapi.ApplyRequest) (*diffapi.ApplyResponse, error) {
	desc := toDescriptor(er.Diff)
	// TODO: Check for supported media types

	mounts := toMounts(er.Mounts)

	mounts, err := s.resolveMounts(ctx, mounts)
	if err != nil {
		return nil, err
	}

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

	aMounts, err := s.resolveMounts(ctx, aMounts)
	if err != nil {
		return nil, err
	}
	bMounts, err = s.resolveMounts(ctx, bMounts)
	if err != nil {
		return nil, err
	}

	ocidesc, err := s.diff.DiffMounts(ctx, aMounts, bMounts, dr.MediaType, dr.Ref)
	if err != nil {
		return nil, err
	}

	return &diffapi.DiffResponse{
		Diff: fromDescriptor(ocidesc),
	}, nil
}

func (s *service) resolveMounts(ctx context.Context, mounts []mount.Mount) ([]mount.Mount, error) {
	resolved := make([]mount.Mount, 0, len(mounts))
	for _, m := range mounts {
		if m.Type == "snapshot" {
			snMounts, err := s.snapshotter.Mounts(ctx, m.Source)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, snMounts...)
		} else {
			resolved = append(resolved, m)
		}
	}
	return resolved, nil
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
