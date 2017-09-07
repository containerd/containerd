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

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/cri-o/ocicni/pkg/ocicni"
	"github.com/golang/glog"
	"google.golang.org/grpc"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
	"k8s.io/kubernetes/pkg/kubelet/server/streaming"

	osinterface "github.com/kubernetes-incubator/cri-containerd/pkg/os"
	"github.com/kubernetes-incubator/cri-containerd/pkg/registrar"
	containerstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/container"
	imagestore "github.com/kubernetes-incubator/cri-containerd/pkg/store/image"
	sandboxstore "github.com/kubernetes-incubator/cri-containerd/pkg/store/sandbox"
)

const (
	// k8sContainerdNamespace is the namespace we use to connect containerd.
	k8sContainerdNamespace = "k8s.io"
	// unixProtocol is the network protocol of unix socket.
	unixProtocol = "unix"
)

// CRIContainerdService is the interface implement CRI remote service server.
type CRIContainerdService interface {
	Run() error
	Stop()
	runtime.RuntimeServiceServer
	runtime.ImageServiceServer
}

// criContainerdService implements CRIContainerdService.
type criContainerdService struct {
	// serverAddress is the grpc server unix path.
	serverAddress string
	// server is the grpc server.
	server *grpc.Server
	// os is an interface for all required os operations.
	os osinterface.OS
	// rootDir is the directory for managing cri-containerd files.
	rootDir string
	// sandboxImage is the image to use for sandbox container.
	sandboxImage string
	// snapshotter is the snapshotter to use in containerd.
	snapshotter string
	// sandboxStore stores all resources associated with sandboxes.
	sandboxStore *sandboxstore.Store
	// sandboxNameIndex stores all sandbox names and make sure each name
	// is unique.
	sandboxNameIndex *registrar.Registrar
	// containerStore stores all resources associated with containers.
	containerStore *containerstore.Store
	// containerNameIndex stores all container names and make sure each
	// name is unique.
	containerNameIndex *registrar.Registrar
	// imageStore stores all resources associated with images.
	imageStore *imagestore.Store
	// taskService is containerd tasks client.
	taskService tasks.TasksClient
	// contentStoreService is the containerd content service client.
	contentStoreService content.Store
	// imageStoreService is the containerd service to store and track
	// image metadata.
	imageStoreService images.Store
	// netPlugin is used to setup and teardown network when run/stop pod sandbox.
	netPlugin ocicni.CNIPlugin
	// client is an instance of the containerd client
	client *containerd.Client
	// streamServer is the streaming server serves container streaming request.
	streamServer streaming.Server
	// cgroupPath in which the cri-containerd is placed in
	cgroupPath string
	// eventMonitor is the monitor monitors containerd events.
	eventMonitor *eventMonitor
}

// NewCRIContainerdService returns a new instance of CRIContainerdService
// TODO(random-liu): Add cri-containerd server config to get rid of the long arg list.
func NewCRIContainerdService(
	serverAddress,
	containerdEndpoint,
	containerdSnapshotter,
	rootDir,
	networkPluginBinDir,
	networkPluginConfDir,
	streamAddress,
	streamPort string,
	cgroupPath string,
	sandboxImage string) (CRIContainerdService, error) {
	// TODO(random-liu): [P2] Recover from runtime state and checkpoint.

	client, err := containerd.New(containerdEndpoint, containerd.WithDefaultNamespace(k8sContainerdNamespace))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize containerd client with endpoint %q: %v", containerdEndpoint, err)
	}
	if cgroupPath != "" {
		_, err := loadCgroup(cgroupPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load cgroup for cgroup path %v: %v", cgroupPath, err)
		}
	}

	c := &criContainerdService{
		serverAddress:       serverAddress,
		os:                  osinterface.RealOS{},
		rootDir:             rootDir,
		sandboxImage:        sandboxImage,
		snapshotter:         containerdSnapshotter,
		sandboxStore:        sandboxstore.NewStore(),
		containerStore:      containerstore.NewStore(),
		imageStore:          imagestore.NewStore(),
		sandboxNameIndex:    registrar.NewRegistrar(),
		containerNameIndex:  registrar.NewRegistrar(),
		taskService:         client.TaskService(),
		imageStoreService:   client.ImageService(),
		contentStoreService: client.ContentStore(),
		client:              client,
		cgroupPath:          cgroupPath,
	}

	netPlugin, err := ocicni.InitCNI(networkPluginConfDir, networkPluginBinDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cni plugin: %v", err)
	}
	c.netPlugin = netPlugin

	// prepare streaming server
	c.streamServer, err = newStreamServer(c, streamAddress, streamPort)
	if err != nil {
		return nil, fmt.Errorf("failed to create stream server: %v", err)
	}

	c.eventMonitor, err = newEventMonitor(c)
	if err != nil {
		return nil, fmt.Errorf("failed to create event monitor: %v", err)
	}

	// Create the grpc server and register runtime and image services.
	c.server = grpc.NewServer()
	runtime.RegisterRuntimeServiceServer(c.server, newInstrumentedService(c))
	runtime.RegisterImageServiceServer(c.server, newInstrumentedService(c))

	return newInstrumentedService(c), nil
}

// Run starts the cri-containerd service.
func (c *criContainerdService) Run() error {
	glog.V(2).Info("Start cri-containerd service")
	// TODO(random-liu): Recover state.

	// Start event handler.
	glog.V(2).Info("Start event monitor")
	eventMonitorCloseCh := c.eventMonitor.start()

	// Start streaming server.
	glog.V(2).Info("Start streaming server")
	streamServerCloseCh := make(chan struct{})
	go func() {
		if err := c.streamServer.Start(true); err != nil {
			glog.Errorf("Failed to start streaming server: %v", err)
		}
		close(streamServerCloseCh)
	}()

	// Start grpc server.
	// Unlink to cleanup the previous socket file.
	glog.V(2).Info("Start grpc server")
	err := syscall.Unlink(c.serverAddress)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to unlink socket file %q: %v", c.serverAddress, err)
	}
	l, err := net.Listen(unixProtocol, c.serverAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %q: %v", c.serverAddress, err)
	}
	grpcServerCloseCh := make(chan struct{})
	go func() {
		if err := c.server.Serve(l); err != nil {
			glog.Errorf("Failed to serve grpc grpc request: %v", err)
		}
		close(grpcServerCloseCh)
	}()

	// Stop the whole cri-containerd service if any of the critical service exits.
	select {
	case <-eventMonitorCloseCh:
	case <-streamServerCloseCh:
	case <-grpcServerCloseCh:
	}
	c.Stop()

	<-eventMonitorCloseCh
	glog.V(2).Info("Event monitor stopped")
	<-streamServerCloseCh
	glog.V(2).Info("Stream server stopped")
	<-grpcServerCloseCh
	glog.V(2).Info("GRPC server stopped")
	return nil
}

// Stop stops the cri-containerd service.
func (c *criContainerdService) Stop() {
	glog.V(2).Info("Stop cri-containerd service")
	c.eventMonitor.stop()
	c.streamServer.Stop() // nolint: errcheck
	c.server.Stop()
}
