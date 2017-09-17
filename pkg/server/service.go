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

	"github.com/kubernetes-incubator/cri-containerd/cmd/cri-containerd/options"
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
	// config contains all configurations.
	config options.Config
	// server is the grpc server.
	server *grpc.Server
	// os is an interface for all required os operations.
	os osinterface.OS
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
	// eventMonitor is the monitor monitors containerd events.
	eventMonitor *eventMonitor
}

// NewCRIContainerdService returns a new instance of CRIContainerdService
func NewCRIContainerdService(config options.Config) (CRIContainerdService, error) {
	// TODO(random-liu): [P2] Recover from runtime state and checkpoint.

	client, err := containerd.New(config.ContainerdEndpoint, containerd.WithDefaultNamespace(k8sContainerdNamespace))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize containerd client with endpoint %q: %v",
			config.ContainerdEndpoint, err)
	}
	if config.CgroupPath != "" {
		_, err := loadCgroup(config.CgroupPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load cgroup for cgroup path %v: %v", config.CgroupPath, err)
		}
	}

	c := &criContainerdService{
		config:              config,
		os:                  osinterface.RealOS{},
		sandboxStore:        sandboxstore.NewStore(),
		containerStore:      containerstore.NewStore(),
		imageStore:          imagestore.NewStore(),
		sandboxNameIndex:    registrar.NewRegistrar(),
		containerNameIndex:  registrar.NewRegistrar(),
		taskService:         client.TaskService(),
		imageStoreService:   client.ImageService(),
		contentStoreService: client.ContentStore(),
		client:              client,
	}

	netPlugin, err := ocicni.InitCNI(config.NetworkPluginConfDir, config.NetworkPluginBinDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cni plugin: %v", err)
	}
	c.netPlugin = netPlugin

	// prepare streaming server
	c.streamServer, err = newStreamServer(c, config.StreamServerAddress, config.StreamServerPort)
	if err != nil {
		return nil, fmt.Errorf("failed to create stream server: %v", err)
	}

	c.eventMonitor = newEventMonitor(c)

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
	err := syscall.Unlink(c.config.SocketPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to unlink socket file %q: %v", c.config.SocketPath, err)
	}
	l, err := net.Listen(unixProtocol, c.config.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on %q: %v", c.config.SocketPath, err)
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
