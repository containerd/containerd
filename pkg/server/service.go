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

	contentapi "github.com/containerd/containerd/api/services/content"
	"github.com/containerd/containerd/api/services/execution"
	imagesapi "github.com/containerd/containerd/api/services/images"
	rootfsapi "github.com/containerd/containerd/api/services/rootfs"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/rootfs"
	contentservice "github.com/containerd/containerd/services/content"
	imagesservice "github.com/containerd/containerd/services/images"
	rootfsservice "github.com/containerd/containerd/services/rootfs"
	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata/store"
	"k8s.io/kubernetes/pkg/kubelet/api/v1alpha1/runtime"

	// TODO remove the underscores from the following imports as the services are
	// implemented. "_" is being used to hold the reference to keep autocomplete
	// from deleting them until referenced below.
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
type criContainerdService struct {
	containerService   execution.ContainerServiceClient
	imageStore         images.Store
	contentIngester    content.Ingester
	contentProvider    content.Provider
	rootfsUnpacker     rootfs.Unpacker
	imageMetadataStore metadata.ImageMetadataStore
}

// NewCRIContainerdService returns a new instance of CRIContainerdService
func NewCRIContainerdService(conn *grpc.ClientConn) CRIContainerdService {
	// TODO: Initialize different containerd clients.
	return &criContainerdService{
		containerService:   execution.NewContainerServiceClient(conn),
		imageStore:         imagesservice.NewStoreFromClient(imagesapi.NewImagesClient(conn)),
		contentIngester:    contentservice.NewIngesterFromClient(contentapi.NewContentClient(conn)),
		contentProvider:    contentservice.NewProviderFromClient(contentapi.NewContentClient(conn)),
		rootfsUnpacker:     rootfsservice.NewUnpackerFromClient(rootfsapi.NewRootFSClient(conn)),
		imageMetadataStore: metadata.NewImageMetadataStore(store.NewMetadataStore()),
	}
}
