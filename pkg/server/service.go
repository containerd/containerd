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

	contentapi "github.com/containerd/containerd/api/services/content"
	"github.com/containerd/containerd/api/services/execution"
	imagesapi "github.com/containerd/containerd/api/services/images"
	rootfsapi "github.com/containerd/containerd/api/services/rootfs"
	versionapi "github.com/containerd/containerd/api/services/version"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	contentservice "github.com/containerd/containerd/services/content"
	imagesservice "github.com/containerd/containerd/services/images"
	"github.com/docker/docker/pkg/truncindex"
	"github.com/kubernetes-incubator/cri-o/pkg/ocicni"
	"google.golang.org/grpc"
	healthapi "google.golang.org/grpc/health/grpc_health_v1"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata/store"
	osinterface "github.com/kubernetes-incubator/cri-containerd/pkg/os"
	"github.com/kubernetes-incubator/cri-containerd/pkg/registrar"
)

// TODO remove the underscores from the following imports as the services are
// implemented. "_" is being used to hold the reference to keep autocomplete
// from deleting them until referenced below.
// nolint: golint
import (
	_ "github.com/containerd/containerd/api/types/container"
	_ "github.com/containerd/containerd/api/types/descriptor"
	_ "github.com/containerd/containerd/api/types/mount"
	_ "github.com/opencontainers/image-spec/specs-go"
	_ "github.com/opencontainers/runtime-spec/specs-go"
)

// CRIContainerdService is the interface implement CRI remote service server.
type CRIContainerdService interface {
	Start()
	runtime.RuntimeServiceServer
	runtime.ImageServiceServer
}

// criContainerdService implements CRIContainerdService.
type criContainerdService struct {
	// os is an interface for all required os operations.
	os osinterface.OS
	// rootDir is the directory for managing cri-containerd files.
	rootDir string
	// sandboxImage is the image to use for sandbox container.
	// TODO(random-liu): Make this configurable via flag.
	sandboxImage string
	// sandboxStore stores all sandbox metadata.
	sandboxStore metadata.SandboxStore
	// imageMetadataStore stores all image metadata.
	imageMetadataStore metadata.ImageMetadataStore
	// sandboxNameIndex stores all sandbox names and make sure each name
	// is unique.
	sandboxNameIndex *registrar.Registrar
	// sandboxIDIndex is trie tree for truncated id indexing, e.g. after an
	// id "abcdefg" is added, we could use "abcd" to identify the same thing
	// as long as there is no ambiguity.
	sandboxIDIndex *truncindex.TruncIndex
	// containerStore stores all container metadata.
	containerStore metadata.ContainerStore
	// containerNameIndex stores all container names and make sure each
	// name is unique.
	containerNameIndex *registrar.Registrar
	// containerService is containerd container service client.
	containerService execution.ContainerServiceClient
	// contentStoreService is the containerd content service client.
	contentStoreService content.Store
	// rootfsService is the containerd rootfs service client.
	rootfsService rootfsapi.RootFSClient
	// imageStoreService is the containerd service to store and track
	// image metadata.
	imageStoreService images.Store
	// versionService is the containerd version service client.
	versionService versionapi.VersionClient
	// healthService is the healthcheck service of containerd grpc server.
	healthService healthapi.HealthClient
	// netPlugin is used to setup and teardown network when run/stop pod sandbox.
	netPlugin ocicni.CNIPlugin
}

// NewCRIContainerdService returns a new instance of CRIContainerdService
func NewCRIContainerdService(conn *grpc.ClientConn, rootDir, networkPluginBinDir, networkPluginConfDir string) (CRIContainerdService, error) {
	// TODO(random-liu): [P2] Recover from runtime state and metadata store.
	c := &criContainerdService{
		os:                 osinterface.RealOS{},
		rootDir:            rootDir,
		sandboxImage:       defaultSandboxImage,
		sandboxStore:       metadata.NewSandboxStore(store.NewMetadataStore()),
		containerStore:     metadata.NewContainerStore(store.NewMetadataStore()),
		imageMetadataStore: metadata.NewImageMetadataStore(store.NewMetadataStore()),
		// TODO(random-liu): Register sandbox/container id/name for recovered sandbox/container.
		// TODO(random-liu): Use the same name and id index for both container and sandbox.
		sandboxNameIndex: registrar.NewRegistrar(),
		sandboxIDIndex:   truncindex.NewTruncIndex(nil),
		// TODO(random-liu): Add container id index.
		containerNameIndex:  registrar.NewRegistrar(),
		containerService:    execution.NewContainerServiceClient(conn),
		imageStoreService:   imagesservice.NewStoreFromClient(imagesapi.NewImagesClient(conn)),
		contentStoreService: contentservice.NewStoreFromClient(contentapi.NewContentClient(conn)),
		rootfsService:       rootfsapi.NewRootFSClient(conn),
		versionService:      versionapi.NewVersionClient(conn),
		healthService:       healthapi.NewHealthClient(conn),
	}

	netPlugin, err := ocicni.InitCNI(networkPluginBinDir, networkPluginConfDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cni plugin: %v", err)
	}
	c.netPlugin = netPlugin

	return c, nil
}

func (c *criContainerdService) Start() {
	c.startEventMonitor()
}
