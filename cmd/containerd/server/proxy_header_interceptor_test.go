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

package server

import (
	"context"
	"reflect"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

func TestProxyHeaderInterceptors(t *testing.T) {
	const (
		ns      = "test-ns"
		leaseID = "test-lease"
	)

	tests := []struct {
		name      string
		ctx       func() context.Context
		wantNS    []string
		wantLease []string
		wantMD    bool
	}{
		{
			name:   "namespace only",
			ctx:    func() context.Context { return namespaces.WithNamespace(context.Background(), ns) },
			wantNS: []string{ns},
			wantMD: true,
		},
		{
			name:      "lease only",
			ctx:       func() context.Context { return leases.WithLease(context.Background(), leaseID) },
			wantLease: []string{leaseID},
			wantMD:    true,
		},
		{
			name: "namespace and lease",
			ctx: func() context.Context {
				ctx := namespaces.WithNamespace(context.Background(), ns)
				return leases.WithLease(ctx, leaseID)
			},
			wantNS:    []string{ns},
			wantLease: []string{leaseID},
			wantMD:    true,
		},
		{
			name: "neither",
			ctx:  context.Background,
		},
		{
			name: "incoming metadata only",
			ctx: func() context.Context {
				return metadata.NewIncomingContext(context.Background(), metadata.Pairs(
					namespaces.GRPCHeader, ns,
					leases.GRPCHeader, leaseID,
				))
			},
			wantNS:    []string{ns},
			wantLease: []string{leaseID},
			wantMD:    true,
		},
		{
			name: "replace outgoing values",
			ctx: func() context.Context {
				ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
					namespaces.GRPCHeader, "old-ns",
					leases.GRPCHeader, "old-lease",
				))
				ctx = namespaces.WithNamespace(ctx, ns)
				return leases.WithLease(ctx, leaseID)
			},
			wantNS:    []string{ns},
			wantLease: []string{leaseID},
			wantMD:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			interceptor := proxyHeaderInterceptor{}

			t.Run("unary", func(t *testing.T) {
				var captured context.Context
				invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
					captured = ctx
					return nil
				}
				if err := interceptor.unary(tc.ctx(), "/test/method", nil, nil, nil, invoker); err != nil {
					t.Fatal(err)
				}
				assertProxyHeaders(t, captured, tc.wantNS, tc.wantLease, tc.wantMD)
			})

			t.Run("stream", func(t *testing.T) {
				var captured context.Context
				streamer := func(ctx context.Context, _ *grpc.StreamDesc, _ *grpc.ClientConn, _ string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
					captured = ctx
					return nil, nil
				}
				if _, err := interceptor.stream(tc.ctx(), nil, nil, "/test/method", streamer); err != nil {
					t.Fatal(err)
				}
				assertProxyHeaders(t, captured, tc.wantNS, tc.wantLease, tc.wantMD)
			})
		})
	}
}

func assertProxyHeaders(t *testing.T, ctx context.Context, wantNS, wantLease []string, wantMD bool) {
	t.Helper()
	md, ok := metadata.FromOutgoingContext(ctx)
	if ok != wantMD {
		t.Fatalf("outgoing metadata present: got %v, want %v", ok, wantMD)
	}
	if got := md.Get(namespaces.GRPCHeader); !reflect.DeepEqual(got, wantNS) {
		t.Errorf("namespace header: got %q, want %q", got, wantNS)
	}
	if got := md.Get(leases.GRPCHeader); !reflect.DeepEqual(got, wantLease) {
		t.Errorf("lease header: got %q, want %q", got, wantLease)
	}
}
