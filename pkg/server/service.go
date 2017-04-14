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
	"google.golang.org/grpc"

	"k8s.io/kubernetes/pkg/kubelet/api/v1alpha1/runtime"

	_ "github.com/containerd/containerd/api/services/content"
	_ "github.com/containerd/containerd/api/services/execution"
	_ "github.com/containerd/containerd/api/services/images"
	_ "github.com/containerd/containerd/api/services/rootfs"
	_ "github.com/containerd/containerd/api/types/container"
	_ "github.com/containerd/containerd/api/types/descriptor"
	_ "github.com/containerd/containerd/api/types/mount"
	_ "github.com/opencontainers/image-spec/specs-go"
	_ "github.com/opencontainers/runtime-spec/specs-go"
)

// CRIContainerdService is the interface implement CRI remote service server.
type CRIContainerdService interface {
	runtime.RuntimeServiceServer
	runtime.ImageServiceServer
}

// criContainerdService implements CRIContainerdService.
type criContainerdService struct{}

func NewCRIContainerdService(conn *grpc.ClientConn) CRIContainerdService {
	// TODO: Initialize different containerd clients.
	return &criContainerdService{}
}
