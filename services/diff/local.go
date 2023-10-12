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

package diff

import (
	"context"
	"fmt"

	diffapi "github.com/containerd/containerd/api/services/diff/v1"
	"github.com/containerd/containerd/diff"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/plugin/registry"
	"github.com/containerd/containerd/plugins"
	"github.com/containerd/containerd/services"
	"github.com/containerd/typeurl/v2"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/grpc"
)

type config struct {
	// Order is the order of preference in which to try diff algorithms, the
	// first differ which is supported is used.
	// Note when multiple differs may be supported, this order will be
	// respected for which is chosen. Each differ should return the same
	// correct output, allowing any ordering to be used to prefer
	// more optimimal implementations.
	Order []string `toml:"default"`
}

type differ interface {
	diff.Comparer
	diff.Applier
}

func init() {
	registry.Register(&plugin.Registration{
		Type: plugins.ServicePlugin,
		ID:   services.DiffService,
		Requires: []plugin.Type{
			plugins.DiffPlugin,
		},
		Config: defaultDifferConfig,
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			differs, err := ic.GetByType(plugins.DiffPlugin)
			if err != nil {
				return nil, err
			}

			orderedNames := ic.Config.(*config).Order
			ordered := make([]differ, len(orderedNames))
			for i, n := range orderedNames {
				differp, ok := differs[n]
				if !ok {
					return nil, fmt.Errorf("needed differ not loaded: %s", n)
				}
				d, err := differp.Instance()
				if err != nil {
					return nil, fmt.Errorf("could not load required differ due plugin init error: %s: %w", n, err)
				}

				ordered[i], ok = d.(differ)
				if !ok {
					return nil, fmt.Errorf("differ does not implement Comparer and Applier interface: %s", n)
				}
			}

			return &local{
				differs: ordered,
			}, nil
		},
	})
}

type local struct {
	differs []differ
}

var _ diffapi.DiffClient = &local{}

func (l *local) Apply(ctx context.Context, er *diffapi.ApplyRequest, _ ...grpc.CallOption) (*diffapi.ApplyResponse, error) {
	var (
		ocidesc ocispec.Descriptor
		err     error
		desc    = oci.DescriptorFromProto(er.Diff)
		mounts  = mount.FromProto(er.Mounts)
	)

	var opts []diff.ApplyOpt
	if er.Payloads != nil {
		payloads := make(map[string]typeurl.Any)
		for k, v := range er.Payloads {
			payloads[k] = v
		}
		opts = append(opts, diff.WithPayloads(payloads))
	}

	for _, differ := range l.differs {
		ocidesc, err = differ.Apply(ctx, desc, mounts, opts...)
		if !errdefs.IsNotImplemented(err) {
			break
		}
	}

	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &diffapi.ApplyResponse{
		Applied: oci.DescriptorToProto(ocidesc),
	}, nil

}

func (l *local) Diff(ctx context.Context, dr *diffapi.DiffRequest, _ ...grpc.CallOption) (*diffapi.DiffResponse, error) {
	var (
		ocidesc ocispec.Descriptor
		err     error
		aMounts = mount.FromProto(dr.Left)
		bMounts = mount.FromProto(dr.Right)
	)

	var opts []diff.Opt
	if dr.MediaType != "" {
		opts = append(opts, diff.WithMediaType(dr.MediaType))
	}
	if dr.Ref != "" {
		opts = append(opts, diff.WithReference(dr.Ref))
	}
	if dr.Labels != nil {
		opts = append(opts, diff.WithLabels(dr.Labels))
	}
	if dr.SourceDateEpoch != nil {
		tm := dr.SourceDateEpoch.AsTime()
		opts = append(opts, diff.WithSourceDateEpoch(&tm))
	}

	for _, d := range l.differs {
		ocidesc, err = d.Compare(ctx, aMounts, bMounts, opts...)
		if !errdefs.IsNotImplemented(err) {
			break
		}
	}
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &diffapi.DiffResponse{
		Diff: oci.DescriptorToProto(ocidesc),
	}, nil
}
