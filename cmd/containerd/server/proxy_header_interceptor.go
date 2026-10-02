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

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

// proxyHeaderInterceptor forwards the caller's namespace and lease to proxy
// plugins. There is no server-side interceptor that copies a lease from incoming
// to outgoing metadata, so otherwise content written under it may be garbage
// collected before it is referenced.
type proxyHeaderInterceptor struct{}

func (proxyHeaderInterceptor) unary(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	return invoker(withProxyHeaders(ctx), method, req, reply, cc, opts...)
}

func (proxyHeaderInterceptor) stream(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	return streamer(withProxyHeaders(ctx), desc, cc, method, opts...)
}

func withProxyHeaders(ctx context.Context) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if ok {
		// The metadata returned by FromOutgoingContext must not be modified.
		md = md.Copy()
	} else {
		md = metadata.MD{}
	}

	if ns, ok := namespaces.Namespace(ctx); ok {
		md.Set(namespaces.GRPCHeader, ns)
	}
	if lid, ok := leases.FromContext(ctx); ok {
		md.Set(leases.GRPCHeader, lid)
	}

	if len(md) == 0 {
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, md)
}
