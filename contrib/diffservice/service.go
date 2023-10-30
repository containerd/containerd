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

package diffservice

import (
	"context"

	diffapi "github.com/containerd/containerd/v2/api/services/diff/v1"
	"github.com/containerd/containerd/v2/diff"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/mount"
	"github.com/containerd/containerd/v2/oci"
	"github.com/containerd/typeurl/v2"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type service struct {
	applier  diff.Applier
	comparer diff.Comparer
	diffapi.UnimplementedDiffServer
}

func FromApplierAndComparer(a diff.Applier, c diff.Comparer) diffapi.DiffServer {
	return &service{
		applier:  a,
		comparer: c,
	}
}
func (s *service) Apply(ctx context.Context, er *diffapi.ApplyRequest) (*diffapi.ApplyResponse, error) {
	if s.applier == nil {
		return nil, errdefs.ToGRPC(errdefs.ErrNotImplemented)
	}

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

	ocidesc, err = s.applier.Apply(ctx, desc, mounts, opts...)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &diffapi.ApplyResponse{
		Applied: oci.DescriptorToProto(ocidesc),
	}, nil
}

func (s *service) Diff(ctx context.Context, dr *diffapi.DiffRequest) (*diffapi.DiffResponse, error) {
	if s.comparer == nil {
		return nil, errdefs.ToGRPC(errdefs.ErrNotImplemented)
	}
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

	ocidesc, err = s.comparer.Compare(ctx, aMounts, bMounts, opts...)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &diffapi.DiffResponse{
		Diff: oci.DescriptorToProto(ocidesc),
	}, nil
}
