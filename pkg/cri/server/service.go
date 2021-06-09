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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/pkg/cri/streaming"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/go-cni"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"

	"github.com/containerd/containerd/pkg/cri/store/label"

	"github.com/containerd/containerd/pkg/atomic"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	imagestore "github.com/containerd/containerd/pkg/cri/store/image"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	snapshotstore "github.com/containerd/containerd/pkg/cri/store/snapshot"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	osinterface "github.com/containerd/containerd/pkg/os"
	"github.com/containerd/containerd/pkg/registrar"
)

// grpcServices are all the grpc services provided by cri containerd.
type grpcServices interface {
	runtime.RuntimeServiceServer
	runtime.ImageServiceServer
}

// CRIService is the interface implement CRI remote service server.
type CRIService interface {
	Run() error
	// io.Closer is used by containerd to gracefully stop cri service.
	io.Closer
	plugin.Service
	grpcServices
}

// criService implements CRIService.
type criService struct {
	// config contains all configurations.
	config criconfig.Config
	// imageFSPath is the path to image filesystem.
	imageFSPath string
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
	// snapshotsSyncer syncs snapshots stat from specified snapshotter service
	// to snapshotStore periodically
	snapshotsSyncer *snapshotsSyncer
	// netPlugin is used to setup and teardown network when run/stop pod sandbox.
	netPlugin cni.CNI
	// client is an instance of the containerd client
	client *containerd.Client
	// streamServer is the streaming server serves container streaming request.
	streamServer streaming.Server
	// eventMonitor is the monitor monitors containerd events.
	eventMonitor *eventMonitor
	// initialized indicates whether the server is initialized. All GRPC services
	// should return error before the server is initialized.
	initialized atomic.Bool
	// cniNetConfMonitor is used to reload cni network conf if there is
	// any valid fs change events from cni network conf dir.
	cniNetConfMonitor *cniNetConfSyncer
	// baseOCISpecs contains cached OCI specs loaded via `Runtime.BaseRuntimeSpec`
	baseOCISpecs map[string]*oci.Spec
	// allCaps is the list of the capabilities.
	// When nil, parsed from CapEff of /proc/self/status.
	allCaps []string // nolint
	// a flag value indicates whether the close of CRI service is intended or not.
	normalClose atomic.Bool
}

// NewCRIService returns a new instance of CRIService
func NewCRIService(config criconfig.Config, client *containerd.Client) (CRIService, error) {
	var err error
	labels := label.NewStore()
	c := &criService{
		config:             config,
		client:             client,
		os:                 osinterface.RealOS{},
		sandboxStore:       sandboxstore.NewStore(labels),
		containerStore:     containerstore.NewStore(labels),
		imageStore:         imagestore.NewStore(client),
		snapshotStore:      snapshotstore.NewStore(),
		sandboxNameIndex:   registrar.NewRegistrar(),
		containerNameIndex: registrar.NewRegistrar(),
		initialized:        atomic.NewBool(false),
		normalClose:        atomic.NewBool(false),
	}

	if client.SnapshotService(c.config.ContainerdConfig.Snapshotter) == nil {
		return nil, errors.Errorf("failed to find snapshotter %q", c.config.ContainerdConfig.Snapshotter)
	}

	c.imageFSPath = imageFSPath(config.ContainerdRootDir, config.ContainerdConfig.Snapshotter)
	logrus.Infof("Get image filesystem path %q", c.imageFSPath)

	if err := c.initPlatform(); err != nil {
		return nil, errors.Wrap(err, "initialize platform")
	}

	// prepare streaming server
	c.streamServer, err = newStreamServer(c, config.StreamServerAddress, config.StreamServerPort, config.StreamIdleTimeout)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create stream server")
	}

	c.eventMonitor = newEventMonitor(c)

	c.cniNetConfMonitor, err = newCNINetConfSyncer(c.config.NetworkPluginConfDir, c.netPlugin, c.cniLoadOptions())
	if err != nil {
		return nil, errors.Wrap(err, "failed to create cni conf monitor")
	}

	c.snapshotsSyncer = newSnapshotsSyncer(
		c.snapshotStore,
		c.client.SnapshotService(c.config.ContainerdConfig.Snapshotter),
		time.Duration(c.config.StatsCollectPeriod)*time.Second,
	)

	// Preload base OCI specs
	c.baseOCISpecs, err = loadBaseOCISpecs(&config)
	if err != nil {
		return nil, err
	}

	return c, nil
}

// Register registers all required services onto a specific grpc server.
// This is used by containerd cri plugin.
func (c *criService) Register(s *grpc.Server) error {
	return c.register(s)
}

// RegisterTCP register all required services onto a GRPC server on TCP.
// This is used by containerd CRI plugin.
func (c *criService) RegisterTCP(s *grpc.Server) error {
	if !c.config.DisableTCPService {
		return c.register(s)
	}
	return nil
}

// Run starts the CRI service.
func (c *criService) Run() error {
	logrus.Info("Start subscribing containerd event")
	c.eventMonitor.subscribe(c.client)

	logrus.Infof("Start recovering state")
	if err := c.recover(ctrdutil.NamespacedContext()); err != nil {
		return errors.Wrap(err, "failed to recover state")
	}

	// Start event handler.
	logrus.Info("Start event monitor")
	eventMonitorErrCh := c.eventMonitor.start()

	// Start snapshot stats syncer
	logrus.Info("Start snapshots syncer")
	c.snapshotsSyncer.start()

	// Start CNI network conf syncer
	logrus.Info("Start cni network conf syncer")
	cniNetConfMonitorErrCh := make(chan error, 1)
	go func() {
		defer close(cniNetConfMonitorErrCh)
		cniNetConfMonitorErrCh <- c.cniNetConfMonitor.syncLoop()
	}()

	// Start streaming server.
	logrus.Info("Start streaming server")
	streamServerErrCh := make(chan error)
	go func() {
		defer close(streamServerErrCh)
		if err := c.streamServer.Start(true); err != nil && err != http.ErrServerClosed {
			logrus.WithError(err).Error("Failed to start streaming server")
			streamServerErrCh <- err
		}
	}()

	// Set the server as initialized. GRPC services could start serving traffic.
	c.initialized.Set()

	var criticalErr error
	var errFrom string
	// Stop the whole CRI service if any of the critical service exits.
	// We only care about the first error which causes the whole CRI service to exit.
	select {
	case criticalErr = <-eventMonitorErrCh:
		errFrom = "event monitor"
	case criticalErr = <-streamServerErrCh:
		errFrom = "stream server"
	case criticalErr = <-cniNetConfMonitorErrCh:
		errFrom = "cni network conf monitor"
	}
	// if this is not an expected close, we need to stop all other services.
	if !c.normalClose.IsSet() {
		if err := c.close(); err != nil {
			return errors.Wrap(err, "failed to stop cri service")
		}
	}
	// The streamServerErrCh should be closed. Or we wait for it to close.
	<-streamServerErrCh
	logrus.Info("Stream server stopped")

	if criticalErr != nil {
		return errors.Wrap(criticalErr, errFrom+" error")
	}
	return nil
}

// Close stops the CRI service.
// TODO(random-liu): Make close synchronous.
func (c *criService) Close() error {
	logrus.Info("Stop CRI service")
	c.normalClose.Set()
	return c.close()
}

// Close the CRI service, but don't print the "Stop CRI service" logs.
func (c *criService) close() error {
	if err := c.cniNetConfMonitor.stop(); err != nil {
		logrus.WithError(err).Error("failed to stop cni network conf monitor")
	}
	c.eventMonitor.stop()
	c.snapshotsSyncer.stop()
	if err := c.streamServer.Stop(); err != nil {
		return errors.Wrap(err, "failed to stop stream server")
	}
	return nil
}

func (c *criService) register(s *grpc.Server) error {
	instrumented := newInstrumentedService(c)
	runtime.RegisterRuntimeServiceServer(s, instrumented)
	runtime.RegisterImageServiceServer(s, instrumented)
	return nil
}

// imageFSPath returns containerd image filesystem path.
// Note that if containerd changes directory layout, we also needs to change this.
func imageFSPath(rootDir, snapshotter string) string {
	return filepath.Join(rootDir, fmt.Sprintf("%s.%s", plugin.SnapshotPlugin, snapshotter))
}

func loadOCISpec(filename string) (*oci.Spec, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open base OCI spec: %s", filename)
	}
	defer file.Close()

	spec := oci.Spec{}
	if err := json.NewDecoder(file).Decode(&spec); err != nil {
		return nil, errors.Wrap(err, "failed to parse base OCI spec file")
	}

	return &spec, nil
}

func loadBaseOCISpecs(config *criconfig.Config) (map[string]*oci.Spec, error) {
	specs := map[string]*oci.Spec{}
	for _, cfg := range config.Runtimes {
		if cfg.BaseRuntimeSpec == "" {
			continue
		}

		// Don't load same file twice
		if _, ok := specs[cfg.BaseRuntimeSpec]; ok {
			continue
		}

		spec, err := loadOCISpec(cfg.BaseRuntimeSpec)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to load base OCI spec from file: %s", cfg.BaseRuntimeSpec)
		}

		specs[cfg.BaseRuntimeSpec] = spec
	}

	return specs, nil
}
