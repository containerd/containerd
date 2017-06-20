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

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/containers"
	"github.com/containerd/containerd/api/services/execution"
	versionapi "github.com/containerd/containerd/api/services/version"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	diffservice "github.com/containerd/containerd/services/diff"
	"github.com/containerd/containerd/snapshot"
	"github.com/docker/docker/pkg/truncindex"
	"github.com/kubernetes-incubator/cri-o/pkg/ocicni"
	healthapi "google.golang.org/grpc/health/grpc_health_v1"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata/store"
	osinterface "github.com/kubernetes-incubator/cri-containerd/pkg/os"
	"github.com/kubernetes-incubator/cri-containerd/pkg/registrar"
	"github.com/kubernetes-incubator/cri-containerd/pkg/server/agents"
)

// k8sContainerdNamespace is the namespace we use to connect containerd.
const k8sContainerdNamespace = "k8s.io"

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
	// containerService is containerd tasks client.
	containerService containers.ContainersClient
	// taskService is containerd tasks client.
	taskService execution.TasksClient
	// contentStoreService is the containerd content service client.
	contentStoreService content.Store
	// snapshotService is the containerd snapshot service client.
	snapshotService snapshot.Snapshotter
	// diffService is the containerd diff service client.
	diffService diffservice.DiffService
	// imageStoreService is the containerd service to store and track
	// image metadata.
	imageStoreService images.Store
	// versionService is the containerd version service client.
	versionService versionapi.VersionClient
	// healthService is the healthcheck service of containerd grpc server.
	healthService healthapi.HealthClient
	// netPlugin is used to setup and teardown network when run/stop pod sandbox.
	netPlugin ocicni.CNIPlugin
	// agentFactory is the factory to create agent used in the cri containerd service.
	agentFactory agents.AgentFactory
	// client is an instance of the containerd client
	client *containerd.Client
}

// NewCRIContainerdService returns a new instance of CRIContainerdService
func NewCRIContainerdService(containerdEndpoint, rootDir, networkPluginBinDir, networkPluginConfDir string) (CRIContainerdService, error) {
	// TODO(random-liu): [P2] Recover from runtime state and metadata store.

	client, err := containerd.New(containerdEndpoint, containerd.WithDefaultNamespace(k8sContainerdNamespace))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize containerd client with endpoint %q: %v", containerdEndpoint, err)
	}

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
		containerService:    client.ContainerService(),
		taskService:         client.TaskService(),
		imageStoreService:   client.ImageService(),
		contentStoreService: client.ContentStore(),
		snapshotService:     client.SnapshotService(),
		diffService:         client.DiffService(),
		versionService:      client.VersionService(),
		healthService:       client.HealthService(),
		agentFactory:        agents.NewAgentFactory(),
		client:              client,
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
