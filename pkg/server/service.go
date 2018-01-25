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
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/plugin"
	"github.com/cri-o/ocicni/pkg/ocicni"
	runcapparmor "github.com/opencontainers/runc/libcontainer/apparmor"
	runcseccomp "github.com/opencontainers/runc/libcontainer/seccomp"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
	"k8s.io/kubernetes/pkg/kubelet/server/streaming"

	"github.com/containerd/cri-containerd/cmd/cri-containerd/options"
	api "github.com/containerd/cri-containerd/pkg/api/v1"
	"github.com/containerd/cri-containerd/pkg/atomic"
	osinterface "github.com/containerd/cri-containerd/pkg/os"
	"github.com/containerd/cri-containerd/pkg/registrar"
	containerstore "github.com/containerd/cri-containerd/pkg/store/container"
	imagestore "github.com/containerd/cri-containerd/pkg/store/image"
	sandboxstore "github.com/containerd/cri-containerd/pkg/store/sandbox"
	snapshotstore "github.com/containerd/cri-containerd/pkg/store/snapshot"
)

const (
	// k8sContainerdNamespace is the namespace we use to connect containerd.
	k8sContainerdNamespace = "k8s.io"
	// unixProtocol is the network protocol of unix socket.
	unixProtocol = "unix"
)

// grpcServices are all the grpc services provided by cri containerd.
type grpcServices interface {
	runtime.RuntimeServiceServer
	runtime.ImageServiceServer
	api.CRIContainerdServiceServer
}

// CRIContainerdService is the interface implement CRI remote service server.
type CRIContainerdService interface {
	Run(bool) error
	// io.Closer is used by containerd to gracefully stop cri service.
	io.Closer
	plugin.Service
	grpcServices
}

// criContainerdService implements CRIContainerdService.
type criContainerdService struct {
	// config contains all configurations.
	config options.Config
	// imageFSUUID is the device uuid of image filesystem.
	imageFSUUID string
	// apparmorEnabled indicates whether apparmor is enabled.
	apparmorEnabled bool
	// seccompEnabled indicates whether seccomp is enabled.
	seccompEnabled bool
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
	// snapshotStore stores information of all snapshots.
	snapshotStore *snapshotstore.Store
	// netPlugin is used to setup and teardown network when run/stop pod sandbox.
	netPlugin ocicni.CNIPlugin
	// client is an instance of the containerd client
	client *containerd.Client
	// streamServer is the streaming server serves container streaming request.
	streamServer streaming.Server
	// eventMonitor is the monitor monitors containerd events.
	eventMonitor *eventMonitor
	// initialized indicates whether the server is initialized. All GRPC services
	// should return error before the server is initialized.
	initialized atomic.Bool
}

// NewCRIContainerdService returns a new instance of CRIContainerdService
func NewCRIContainerdService(config options.Config) (CRIContainerdService, error) {
	var err error
	c := &criContainerdService{
		config:             config,
		apparmorEnabled:    runcapparmor.IsEnabled(),
		seccompEnabled:     runcseccomp.IsEnabled(),
		os:                 osinterface.RealOS{},
		sandboxStore:       sandboxstore.NewStore(),
		containerStore:     containerstore.NewStore(),
		imageStore:         imagestore.NewStore(),
		snapshotStore:      snapshotstore.NewStore(),
		sandboxNameIndex:   registrar.NewRegistrar(),
		containerNameIndex: registrar.NewRegistrar(),
		initialized:        atomic.NewBool(false),
	}

	if c.config.EnableSelinux {
		if !selinux.GetEnabled() {
			logrus.Warn("Selinux is not supported")
		}
	} else {
		selinux.SetDisabled()
	}

	if !c.config.SkipImageFSUUID {
		imageFSPath := imageFSPath(config.ContainerdRootDir, config.ContainerdConfig.Snapshotter)
		c.imageFSUUID, err = c.getDeviceUUID(imageFSPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get imagefs uuid of %q: %v", imageFSPath, err)
		}
		logrus.Infof("Get device uuid %q for image filesystem %q", c.imageFSUUID, imageFSPath)
	} else {
		logrus.Warn("Skip retrieving imagefs UUID, kubelet will not be able to get imagefs capacity or perform imagefs disk eviction.")
	}

	c.netPlugin, err = ocicni.InitCNI(config.NetworkPluginConfDir, config.NetworkPluginBinDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cni plugin: %v", err)
	}

	// prepare streaming server
	c.streamServer, err = newStreamServer(c, config.StreamServerAddress, config.StreamServerPort)
	if err != nil {
		return nil, fmt.Errorf("failed to create stream server: %v", err)
	}

	c.eventMonitor = newEventMonitor(c.containerStore, c.sandboxStore)

	// To avoid race condition between `Run` and `Stop`, still create grpc server
	// although we may not use it. It's just a small in-memory data structure.
	// TODO(random-liu): Get rid of the grpc server when completely switch
	// to plugin mode.
	c.server = grpc.NewServer()

	return c, nil
}

// Register registers all required services onto a specific grpc server.
// This is used by containerd cri plugin.
func (c *criContainerdService) Register(s *grpc.Server) error {
	instrumented := newInstrumentedService(c)
	runtime.RegisterRuntimeServiceServer(s, instrumented)
	runtime.RegisterImageServiceServer(s, instrumented)
	api.RegisterCRIContainerdServiceServer(s, instrumented)
	return nil
}

// Run starts the cri-containerd service. startGRPC specifies
// whether to start grpc server in this function.
// TODO(random-liu): Remove `startRPC=true` case when we no longer support cri-containerd
// standalone mode.
func (c *criContainerdService) Run(startGRPC bool) error {
	logrus.Info("Start cri-containerd service")

	// Connect containerd service here, to get rid of the containerd dependency
	// in `NewCRIContainerdService`. This is required for plugin mode bootstrapping.
	logrus.Info("Connect containerd service")
	client, err := containerd.New(c.config.ContainerdEndpoint, containerd.WithDefaultNamespace(k8sContainerdNamespace))
	if err != nil {
		return fmt.Errorf("failed to initialize containerd client with endpoint %q: %v",
			c.config.ContainerdEndpoint, err)
	}
	c.client = client

	logrus.Info("Start subscribing containerd event")
	c.eventMonitor.subscribe(c.client)

	logrus.Infof("Start recovering state")
	if err := c.recover(context.Background()); err != nil {
		return fmt.Errorf("failed to recover state: %v", err)
	}

	// Start event handler.
	logrus.Info("Start event monitor")
	eventMonitorCloseCh, err := c.eventMonitor.start()
	if err != nil {
		return fmt.Errorf("failed to start event monitor: %v", err)
	}

	// Start snapshot stats syncer, it doesn't need to be stopped.
	logrus.Info("Start snapshots syncer")
	snapshotsSyncer := newSnapshotsSyncer(
		c.snapshotStore,
		c.client.SnapshotService(c.config.ContainerdConfig.Snapshotter),
		time.Duration(c.config.StatsCollectPeriod)*time.Second,
	)
	snapshotsSyncer.start()

	// Start streaming server.
	logrus.Info("Start streaming server")
	streamServerCloseCh := make(chan struct{})
	go func() {
		if err := c.streamServer.Start(true); err != nil {
			logrus.WithError(err).Error("Failed to start streaming server")
		}
		close(streamServerCloseCh)
	}()

	// Set the server as initialized. GRPC services could start serving traffic.
	c.initialized.Set()

	grpcServerCloseCh := make(chan struct{})
	if startGRPC {
		// Create the grpc server and register runtime and image services.
		c.Register(c.server) // nolint: errcheck
		// Start grpc server.
		// Unlink to cleanup the previous socket file.
		logrus.Info("Start grpc server")
		err := syscall.Unlink(c.config.SocketPath)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to unlink socket file %q: %v", c.config.SocketPath, err)
		}
		l, err := net.Listen(unixProtocol, c.config.SocketPath)
		if err != nil {
			return fmt.Errorf("failed to listen on %q: %v", c.config.SocketPath, err)
		}
		go func() {
			if err := c.server.Serve(l); err != nil {
				logrus.WithError(err).Error("Failed to serve grpc request")
			}
			close(grpcServerCloseCh)
		}()
	}
	// Keep grpcServerCloseCh open if grpc server is not started.

	// Stop the whole cri-containerd service if any of the critical service exits.
	select {
	case <-eventMonitorCloseCh:
	case <-streamServerCloseCh:
	case <-grpcServerCloseCh:
	}
	if err := c.Close(); err != nil {
		return fmt.Errorf("failed to stop cri service: %v", err)
	}

	<-eventMonitorCloseCh
	logrus.Info("Event monitor stopped")
	<-streamServerCloseCh
	logrus.Info("Stream server stopped")
	if startGRPC {
		// Only wait for grpc server close channel when grpc server is started.
		<-grpcServerCloseCh
		logrus.Info("GRPC server stopped")
	}
	return nil
}

// Stop stops the cri-containerd service.
func (c *criContainerdService) Close() error {
	logrus.Info("Stop cri-containerd service")
	// TODO(random-liu): Make event monitor stop synchronous.
	c.eventMonitor.stop()
	if err := c.streamServer.Stop(); err != nil {
		return fmt.Errorf("failed to stop stream server: %v", err)
	}
	c.server.Stop()
	return nil
}

// getDeviceUUID gets device uuid for a given path.
func (c *criContainerdService) getDeviceUUID(path string) (string, error) {
	mount, err := c.os.LookupMount(path)
	if err != nil {
		return "", err
	}
	rdev := unix.Mkdev(uint32(mount.Major), uint32(mount.Minor))
	return c.os.DeviceUUID(rdev)
}

// imageFSPath returns containerd image filesystem path.
// Note that if containerd changes directory layout, we also needs to change this.
func imageFSPath(rootDir, snapshotter string) string {
	return filepath.Join(rootDir, fmt.Sprintf("%s.%s", plugin.SnapshotPlugin, snapshotter))
}
