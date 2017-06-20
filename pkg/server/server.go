/*
Copyright 2017 The Kubernetes Authors.

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
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/containerd/containerd/namespaces"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
	"k8s.io/kubernetes/pkg/util/interrupt"
)

const (
	// unixProtocol is the network protocol of unix socket.
	unixProtocol = "unix"
	// k8sContainerdNamespace is the namespace we use to connect containerd.
	k8sContainerdNamespace = "k8s.io"
)

// CRIContainerdServer is the grpc server of cri-containerd.
type CRIContainerdServer struct {
	// addr is the address to serve on.
	addr string
	// runtimeService is the cri-containerd runtime service.
	runtimeService runtime.RuntimeServiceServer
	// imageService is the cri-containerd image service.
	imageService runtime.ImageServiceServer
	// server is the grpc server.
	server *grpc.Server
}

// NewCRIContainerdServer creates the cri-containerd grpc server.
func NewCRIContainerdServer(addr string, r runtime.RuntimeServiceServer, i runtime.ImageServiceServer) *CRIContainerdServer {
	return &CRIContainerdServer{
		addr:           addr,
		runtimeService: r,
		imageService:   i,
	}
}

// Run runs the cri-containerd grpc server.
func (s *CRIContainerdServer) Run() error {
	glog.V(2).Infof("Start cri-containerd grpc server")
	// Unlink to cleanup the previous socket file.
	err := syscall.Unlink(s.addr)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to unlink socket file %q: %v", s.addr, err)
	}
	l, err := net.Listen(unixProtocol, s.addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %q: %v", s.addr, err)
	}
	// Create the grpc server and register runtime and image services.
	s.server = grpc.NewServer()
	runtime.RegisterRuntimeServiceServer(s.server, s.runtimeService)
	runtime.RegisterImageServiceServer(s.server, s.imageService)
	// Use interrupt handler to make sure the server to be stopped properly.
	h := interrupt.New(nil, s.server.Stop)
	return h.Run(func() error { return s.server.Serve(l) })
}

// ConnectToContainerd returns a grpc client for containerd.
func ConnectToContainerd(path string, connectionTimeout time.Duration) (*grpc.ClientConn, error) {
	// get the containerd client
	dialOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithTimeout(connectionTimeout),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout(unixProtocol, path, timeout)
		}),
		grpc.WithUnaryInterceptor(grpc.UnaryClientInterceptor(unary)),
		grpc.WithStreamInterceptor(grpc.StreamClientInterceptor(stream)),
	}
	return grpc.Dial(fmt.Sprintf("%s://%s", unixProtocol, path), dialOpts...)
}

// TODO(random-liu): Get rid of following functions after switching to containerd client.
// unary is a wrapper to apply kubernetes namespace in each grpc unary call.
func unary(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	_, ok := namespaces.Namespace(ctx)
	if !ok {
		ctx = namespaces.WithNamespace(ctx, k8sContainerdNamespace)
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

// stream is a wrapper to apply kubernetes namespace in each grpc stream call.
func stream(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	_, ok := namespaces.Namespace(ctx)
	if !ok {
		ctx = namespaces.WithNamespace(ctx, k8sContainerdNamespace)
	}

	return streamer(ctx, desc, cc, method, opts...)
}
