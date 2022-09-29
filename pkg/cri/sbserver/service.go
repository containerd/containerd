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

package sbserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/pkg/atomic"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/sbserver/podsandbox"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	imagestore "github.com/containerd/containerd/pkg/cri/store/image"
	"github.com/containerd/containerd/pkg/cri/store/label"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	snapshotstore "github.com/containerd/containerd/pkg/cri/store/snapshot"
	"github.com/containerd/containerd/pkg/cri/streaming"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	"github.com/containerd/containerd/pkg/kmutex"
	osinterface "github.com/containerd/containerd/pkg/os"
	"github.com/containerd/containerd/pkg/registrar"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/sandbox"

	"github.com/containerd/go-cni"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	runtime_alpha "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

// defaultNetworkPlugin is used for the default CNI configuration
const defaultNetworkPlugin = "default"

// grpcServices are all the grpc services provided by cri containerd.
type grpcServices interface {
	runtime.RuntimeServiceServer
	runtime.ImageServiceServer
}

type grpcAlphaServices interface {
	runtime_alpha.RuntimeServiceServer
	runtime_alpha.ImageServiceServer
}

// CRIService is the interface implement CRI remote service server.
type CRIService interface {
	Run() error
	// io.Closer is used by containerd to gracefully stop cri service.
	io.Closer
	Register(*grpc.Server) error
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
	// sandboxController controls sandbox lifecycle (and hides implementation details behind).
	sandboxController sandbox.Controller
	// containerNameIndex stores all container names and make sure each
	// name is unique.
	containerNameIndex *registrar.Registrar
	// imageStore stores all resources associated with images.
	imageStore *imagestore.Store
	// snapshotStore stores information of all snapshots.
	snapshotStore *snapshotstore.Store
	// netPlugin is used to setup and teardown network when run/stop pod sandbox.
	netPlugin map[string]cni.CNI
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
	cniNetConfMonitor map[string]*cniNetConfSyncer
	// baseOCISpecs contains cached OCI specs loaded via `Runtime.BaseRuntimeSpec`
	baseOCISpecs map[string]*oci.Spec
	// allCaps is the list of the capabilities.
	// When nil, parsed from CapEff of /proc/self/status.
	allCaps []string // nolint
	// unpackDuplicationSuppressor is used to make sure that there is only
	// one in-flight fetch request or unpack handler for a given descriptor's
	// or chain ID.
	unpackDuplicationSuppressor kmutex.KeyedLocker
}

// NewCRIService returns a new instance of CRIService
func NewCRIService(config criconfig.Config, client *containerd.Client) (CRIService, error) {
	var err error
	labels := label.NewStore()
	c := &criService{
		config:                      config,
		client:                      client,
		os:                          osinterface.RealOS{},
		sandboxStore:                sandboxstore.NewStore(labels),
		containerStore:              containerstore.NewStore(labels),
		imageStore:                  imagestore.NewStore(client),
		snapshotStore:               snapshotstore.NewStore(),
		sandboxNameIndex:            registrar.NewRegistrar(),
		containerNameIndex:          registrar.NewRegistrar(),
		initialized:                 atomic.NewBool(false),
		netPlugin:                   make(map[string]cni.CNI),
		unpackDuplicationSuppressor: kmutex.New(),
	}

	if client.SnapshotService(c.config.ContainerdConfig.Snapshotter) == nil {
		return nil, fmt.Errorf("failed to find snapshotter %q", c.config.ContainerdConfig.Snapshotter)
	}

	c.imageFSPath = imageFSPath(config.ContainerdRootDir, config.ContainerdConfig.Snapshotter)
	logrus.Infof("Get image filesystem path %q", c.imageFSPath)

	if err := c.initPlatform(); err != nil {
		return nil, fmt.Errorf("initialize platform: %w", err)
	}

	// prepare streaming server
	c.streamServer, err = newStreamServer(c, config.StreamServerAddress, config.StreamServerPort, config.StreamIdleTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create stream server: %w", err)
	}

	c.eventMonitor = newEventMonitor(c)

	c.cniNetConfMonitor = make(map[string]*cniNetConfSyncer)
	for name, i := range c.netPlugin {
		path := c.config.NetworkPluginConfDir
		if name != defaultNetworkPlugin {
			if rc, ok := c.config.Runtimes[name]; ok {
				path = rc.NetworkPluginConfDir
			}
		}
		if path != "" {
			m, err := newCNINetConfSyncer(path, i, c.cniLoadOptions())
			if err != nil {
				return nil, fmt.Errorf("failed to create cni conf monitor for %s: %w", name, err)
			}
			c.cniNetConfMonitor[name] = m
		}
	}

	// Preload base OCI specs
	c.baseOCISpecs, err = loadBaseOCISpecs(&config)
	if err != nil {
		return nil, err
	}

	c.sandboxController = podsandbox.New(config, client, c.sandboxStore, c.os, c, c.baseOCISpecs)

	return c, nil
}

// BackOffEvent is a temporary workaround to call eventMonitor from controller.Stop.
// TODO: get rid of this.
func (c *criService) BackOffEvent(id string, event interface{}) {
	c.eventMonitor.backOff.enBackOff(id, event)
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
		return fmt.Errorf("failed to recover state: %w", err)
	}

	// Start event handler.
	logrus.Info("Start event monitor")
	eventMonitorErrCh := c.eventMonitor.start()

	// Start snapshot stats syncer, it doesn't need to be stopped.
	logrus.Info("Start snapshots syncer")
	snapshotsSyncer := newSnapshotsSyncer(
		c.snapshotStore,
		c.client.SnapshotService(c.config.ContainerdConfig.Snapshotter),
		time.Duration(c.config.StatsCollectPeriod)*time.Second,
	)
	snapshotsSyncer.start()

	// Start CNI network conf syncers
	cniNetConfMonitorErrCh := make(chan error, len(c.cniNetConfMonitor))
	var netSyncGroup sync.WaitGroup
	for name, h := range c.cniNetConfMonitor {
		netSyncGroup.Add(1)
		logrus.Infof("Start cni network conf syncer for %s", name)
		go func(h *cniNetConfSyncer) {
			cniNetConfMonitorErrCh <- h.syncLoop()
			netSyncGroup.Done()
		}(h)
	}
	go func() {
		netSyncGroup.Wait()
		close(cniNetConfMonitorErrCh)
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

	var eventMonitorErr, streamServerErr, cniNetConfMonitorErr error
	// Stop the whole CRI service if any of the critical service exits.
	select {
	case eventMonitorErr = <-eventMonitorErrCh:
	case streamServerErr = <-streamServerErrCh:
	case cniNetConfMonitorErr = <-cniNetConfMonitorErrCh:
	}
	if err := c.Close(); err != nil {
		return fmt.Errorf("failed to stop cri service: %w", err)
	}
	// If the error is set above, err from channel must be nil here, because
	// the channel is supposed to be closed. Or else, we wait and set it.
	if err := <-eventMonitorErrCh; err != nil {
		eventMonitorErr = err
	}
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
	case err := <-streamServerErrCh:
		if err != nil {
			streamServerErr = err
		}
		logrus.Info("Stream server stopped")
	case <-time.After(streamServerStopTimeout):
		logrus.Errorf("Stream server is not stopped in %q", streamServerStopTimeout)
	}
	if eventMonitorErr != nil {
		return fmt.Errorf("event monitor error: %w", eventMonitorErr)
	}
	if streamServerErr != nil {
		return fmt.Errorf("stream server error: %w", streamServerErr)
	}
	if cniNetConfMonitorErr != nil {
		return fmt.Errorf("cni network conf monitor error: %w", cniNetConfMonitorErr)
	}
	return nil
}

// Close stops the CRI service.
// TODO(random-liu): Make close synchronous.
func (c *criService) Close() error {
	logrus.Info("Stop CRI service")
	for name, h := range c.cniNetConfMonitor {
		if err := h.stop(); err != nil {
			logrus.WithError(err).Errorf("failed to stop cni network conf monitor for %s", name)
		}
	}
	c.eventMonitor.stop()
	if err := c.streamServer.Stop(); err != nil {
		return fmt.Errorf("failed to stop stream server: %w", err)
	}
	return nil
}

func (c *criService) register(s *grpc.Server) error {
	instrumented := newInstrumentedService(c)
	runtime.RegisterRuntimeServiceServer(s, instrumented)
	runtime.RegisterImageServiceServer(s, instrumented)
	instrumentedAlpha := newInstrumentedAlphaService(c)
	runtime_alpha.RegisterRuntimeServiceServer(s, instrumentedAlpha)
	runtime_alpha.RegisterImageServiceServer(s, instrumentedAlpha)
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
		return nil, fmt.Errorf("failed to open base OCI spec: %s: %w", filename, err)
	}
	defer file.Close()

	spec := oci.Spec{}
	if err := json.NewDecoder(file).Decode(&spec); err != nil {
		return nil, fmt.Errorf("failed to parse base OCI spec file: %w", err)
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
			return nil, fmt.Errorf("failed to load base OCI spec from file: %s: %w", cfg.BaseRuntimeSpec, err)
		}

		specs[cfg.BaseRuntimeSpec] = spec
	}

	return specs, nil
}
