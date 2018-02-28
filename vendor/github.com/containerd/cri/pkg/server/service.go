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
	"path/filepath"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/plugin"
	"github.com/cri-o/ocicni/pkg/ocicni"
	runcapparmor "github.com/opencontainers/runc/libcontainer/apparmor"
	runcseccomp "github.com/opencontainers/runc/libcontainer/seccomp"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"
	"k8s.io/kubernetes/pkg/kubelet/server/streaming"

	api "github.com/containerd/cri/pkg/api/v1"
	"github.com/containerd/cri/pkg/atomic"
	criconfig "github.com/containerd/cri/pkg/config"
	ctrdutil "github.com/containerd/cri/pkg/containerd/util"
	osinterface "github.com/containerd/cri/pkg/os"
	"github.com/containerd/cri/pkg/registrar"
	containerstore "github.com/containerd/cri/pkg/store/container"
	imagestore "github.com/containerd/cri/pkg/store/image"
	sandboxstore "github.com/containerd/cri/pkg/store/sandbox"
	snapshotstore "github.com/containerd/cri/pkg/store/snapshot"
)

// grpcServices are all the grpc services provided by cri containerd.
type grpcServices interface {
	runtime.RuntimeServiceServer
	runtime.ImageServiceServer
	api.CRIContainerdServiceServer
}

// CRIContainerdService is the interface implement CRI remote service server.
type CRIContainerdService interface {
	Run() error
	// io.Closer is used by containerd to gracefully stop cri service.
	io.Closer
	plugin.Service
	grpcServices
}

// criContainerdService implements CRIContainerdService.
type criContainerdService struct {
	// config contains all configurations.
	config criconfig.Config
	// imageFSPath is the path to image filesystem.
	imageFSPath string
	// apparmorEnabled indicates whether apparmor is enabled.
	apparmorEnabled bool
	// seccompEnabled indicates whether seccomp is enabled.
	seccompEnabled bool
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
func NewCRIContainerdService(config criconfig.Config, client *containerd.Client) (CRIContainerdService, error) {
	var err error
	c := &criContainerdService{
		config:             config,
		client:             client,
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

	c.imageFSPath = imageFSPath(config.ContainerdRootDir, config.ContainerdConfig.Snapshotter)
	logrus.Infof("Get image filesystem path %q", c.imageFSPath)

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

// Run starts the cri-containerd service.
func (c *criContainerdService) Run() error {
	logrus.Info("Start subscribing containerd event")
	c.eventMonitor.subscribe(c.client)

	logrus.Infof("Start recovering state")
	if err := c.recover(ctrdutil.NamespacedContext()); err != nil {
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

	// Stop the whole cri-containerd service if any of the critical service exits.
	select {
	case <-eventMonitorCloseCh:
	case <-streamServerCloseCh:
	}
	if err := c.Close(); err != nil {
		return fmt.Errorf("failed to stop cri service: %v", err)
	}

	<-eventMonitorCloseCh
	logrus.Info("Event monitor stopped")
	// There is a race condition with http.Server.Serve.
	// When `Close` is called at the same time with `Serve`, `Close`
	// may finish first, and `Serve` may still block.
	// See https://github.com/golang/go/issues/20239.
	// Here we set a 2 second timeout for the stream server wait,
	// if it timeout, an error log is generated.
	// TODO(random-liu): Get rid of this after https://github.com/golang/go/issues/20239
	// is fixed.
	const streamServerStopTimeout = 2 * time.Second
	select {
	case <-streamServerCloseCh:
		logrus.Info("Stream server stopped")
	case <-time.After(streamServerStopTimeout):
		logrus.Errorf("Stream server is not stopped in %q", streamServerStopTimeout)
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
	return nil
}

// imageFSPath returns containerd image filesystem path.
// Note that if containerd changes directory layout, we also needs to change this.
func imageFSPath(rootDir, snapshotter string) string {
	return filepath.Join(rootDir, fmt.Sprintf("%s.%s", plugin.SnapshotPlugin, snapshotter))
}
