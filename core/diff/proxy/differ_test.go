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

package proxy

import (
	"context"
	"testing"

	diffapi "github.com/containerd/containerd/api/services/diff/v1"
	"github.com/containerd/containerd/api/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

type captureClient struct {
	ctx context.Context
}

func (c *captureClient) Apply(ctx context.Context, _ *diffapi.ApplyRequest, _ ...grpc.CallOption) (*diffapi.ApplyResponse, error) {
	c.ctx = ctx
	return &diffapi.ApplyResponse{Applied: &types.Descriptor{}}, nil
}

func (c *captureClient) Diff(ctx context.Context, _ *diffapi.DiffRequest, _ ...grpc.CallOption) (*diffapi.DiffResponse, error) {
	c.ctx = ctx
	return &diffapi.DiffResponse{Diff: &types.Descriptor{}}, nil
}

func TestProxyDifferPropagatesHeaders(t *testing.T) {
	const (
		ns      = "test-ns"
		leaseID = "test-lease"
	)

	for _, tc := range []struct {
		name        string
		ctx         func() context.Context
		wantNS      string
		wantLease   string
	}{
		{
			name:    "namespace only",
			ctx:     func() context.Context { return namespaces.WithNamespace(context.Background(), ns) },
			wantNS:  ns,
		},
		{
			name:      "lease only",
			ctx:       func() context.Context { return leases.WithLease(context.Background(), leaseID) },
			wantLease: leaseID,
		},
		{
			name: "namespace and lease",
			ctx: func() context.Context {
				ctx := namespaces.WithNamespace(context.Background(), ns)
				return leases.WithLease(ctx, leaseID)
			},
			wantNS:    ns,
			wantLease: leaseID,
		},
		{
			name: "neither",
			ctx:  func() context.Context { return context.Background() },
		},
		{
			name: "incoming metadata only",
			ctx: func() context.Context {
				md := metadata.Pairs(
					namespaces.GRPCHeader, ns,
					leases.GRPCHeader, leaseID,
				)
				return metadata.NewIncomingContext(context.Background(), md)
			},
			wantNS:    ns,
			wantLease: leaseID,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("Apply", func(t *testing.T) {
				cc := &captureClient{}
				r := &diffRemote{client: cc}
				if _, err := r.Apply(tc.ctx(), ocispec.Descriptor{}, []mount.Mount{}); err != nil {
					t.Fatalf("Apply: %v", err)
				}
				assertOutgoing(t, cc.ctx, tc.wantNS, tc.wantLease)
			})
			t.Run("Compare", func(t *testing.T) {
				cc := &captureClient{}
				r := &diffRemote{client: cc}
				if _, err := r.Compare(tc.ctx(), []mount.Mount{}, []mount.Mount{}, func(*diff.Config) error { return nil }); err != nil {
					t.Fatalf("Compare: %v", err)
				}
				assertOutgoing(t, cc.ctx, tc.wantNS, tc.wantLease)
			})
		})
	}
}

func assertOutgoing(t *testing.T, ctx context.Context, wantNS, wantLease string) {
	t.Helper()
	md, _ := metadata.FromOutgoingContext(ctx)

	gotNS := firstOrEmpty(md.Get(namespaces.GRPCHeader))
	if gotNS != wantNS {
		t.Errorf("namespace header: got %q, want %q", gotNS, wantNS)
	}

	gotLease := firstOrEmpty(md.Get(leases.GRPCHeader))
	if gotLease != wantLease {
		t.Errorf("lease header: got %q, want %q", gotLease, wantLease)
	}
}

func firstOrEmpty(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
