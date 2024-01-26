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
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/containerd/go-cni"
	"github.com/containerd/log"
	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	"k8s.io/kubelet/pkg/cri/streaming"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/oci"
	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/containerd/containerd/v2/pkg/cri/instrument"
	"github.com/containerd/containerd/v2/pkg/cri/nri"
	"github.com/containerd/containerd/v2/pkg/cri/server/base"
	"github.com/containerd/containerd/v2/pkg/cri/server/podsandbox"
	containerstore "github.com/containerd/containerd/v2/pkg/cri/store/container"
	imagestore "github.com/containerd/containerd/v2/pkg/cri/store/image"
	"github.com/containerd/containerd/v2/pkg/cri/store/label"
	sandboxstore "github.com/containerd/containerd/v2/pkg/cri/store/sandbox"
	snapshotstore "github.com/containerd/containerd/v2/pkg/cri/store/snapshot"
	ctrdutil "github.com/containerd/containerd/v2/pkg/cri/util"
	osinterface "github.com/containerd/containerd/v2/pkg/os"
	"github.com/containerd/containerd/v2/pkg/registrar"
	"github.com/containerd/containerd/v2/sandbox"
)

// defaultNetworkPlugin is used for the default CNI configuration
const defaultNetworkPlugin = "default"

// CRIService is the interface implement CRI remote service server.
type CRIService interface {
	runtime.RuntimeServiceServer
	runtime.ImageServiceServer
	// Closer is used by containerd to gracefully stop cri service.
	io.Closer

	Run(ready func()) error

	Register(*grpc.Server) error
}

type sandboxService interface {
	SandboxController(config *runtime.PodSandboxConfig, runtimeHandler string) (sandbox.Controller, error)
}

// imageService specifies dependencies to image service.
type imageService interface {
	runtime.ImageServiceServer

	RuntimeSnapshotter(ctx context.Context, ociRuntime criconfig.Runtime) string

	UpdateImage(ctx context.Context, r string) error

	GetImage(id string) (imagestore.Image, error)
	GetSnapshot(key, snapshotter string) (snapshotstore.Snapshot, error)

	LocalResolve(refOrID string) (imagestore.Image, error)

	ImageFSPaths() map[string]string
}

// criService implements CRIService.
type criService struct {
	imageService
	// config contains all configurations.
	config criconfig.Config
	// imageFSPaths contains path to image filesystem for snapshotters.
	imageFSPaths map[string]string
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
	allCaps []string //nolint:nolintlint,unused // Ignore on non-Linux
	// containerEventsChan is used to capture container events and send them
	// to the caller of GetContainerEvents.
	containerEventsChan chan runtime.ContainerEventResponse
	// nri is used to hook NRI into CRI request processing.
	nri *nri.API
	// sandboxService is the sandbox related service for CRI
	sandboxService sandboxService
}

// NewCRIService returns a new instance of CRIService
func NewCRIService(criBase *base.CRIBase, imageService imageService, client *containerd.Client, nri *nri.API) (CRIService, error) {
	var err error
	labels := label.NewStore()
	config := criBase.Config
	c := &criService{
		imageService:       imageService,
		config:             config,
		client:             client,
		imageFSPaths:       imageService.ImageFSPaths(),
		os:                 osinterface.RealOS{},
		baseOCISpecs:       criBase.BaseOCISpecs,
		sandboxStore:       sandboxstore.NewStore(labels),
		containerStore:     containerstore.NewStore(labels),
		sandboxNameIndex:   registrar.NewRegistrar(),
		containerNameIndex: registrar.NewRegistrar(),
		netPlugin:          make(map[string]cni.CNI),
		sandboxService:     newCriSandboxService(&config, client),
	}

	// TODO: figure out a proper channel size.
	c.containerEventsChan = make(chan runtime.ContainerEventResponse, 1000)

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

	podSandboxController := client.SandboxController(string(criconfig.ModePodSandbox)).(*podsandbox.Controller)
	podSandboxController.Init(c.sandboxStore, c)

	c.nri = nri

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
func (c *criService) Run(ready func()) error {
	log.L.Info("Start subscribing containerd event")
	c.eventMonitor.subscribe(c.client)

	log.L.Infof("Start recovering state")
	if err := c.recover(ctrdutil.NamespacedContext()); err != nil {
		return fmt.Errorf("failed to recover state: %w", err)
	}

	// Start event handler.
	log.L.Info("Start event monitor")
	eventMonitorErrCh := c.eventMonitor.start()

	// Start CNI network conf syncers
	cniNetConfMonitorErrCh := make(chan error, len(c.cniNetConfMonitor))
	var netSyncGroup sync.WaitGroup
	for name, h := range c.cniNetConfMonitor {
		netSyncGroup.Add(1)
		log.L.Infof("Start cni network conf syncer for %s", name)
		go func(h *cniNetConfSyncer) {
			cniNetConfMonitorErrCh <- h.syncLoop()
			netSyncGroup.Done()
		}(h)
	}
	// For platforms that may not support CNI (darwin etc.) there's no
	// use in launching this as `Wait` will return immediately. Further
	// down we select on this channel along with some others to determine
	// if we should Close() the CRI service, so closing this preemptively
	// isn't good.
	if len(c.cniNetConfMonitor) > 0 {
		go func() {
			netSyncGroup.Wait()
			close(cniNetConfMonitorErrCh)
		}()
	}

	// Start streaming server.
	log.L.Info("Start streaming server")
	streamServerErrCh := make(chan error)
	go func() {
		defer close(streamServerErrCh)
		if err := c.streamServer.Start(true); err != nil && err != http.ErrServerClosed {
			log.L.WithError(err).Error("Failed to start streaming server")
			streamServerErrCh <- err
		}
	}()

	// register CRI domain with NRI
	if err := c.nri.Register(&criImplementation{c}); err != nil {
		return fmt.Errorf("failed to set up NRI for CRI service: %w", err)
	}

	// Set the server as initialized. GRPC services could start serving traffic.
	c.initialized.Store(true)
	ready()

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
	log.L.Info("Event monitor stopped")
	if err := <-streamServerErrCh; err != nil {
		streamServerErr = err
	}
	log.L.Info("Stream server stopped")
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
	log.L.Info("Stop CRI service")
	for name, h := range c.cniNetConfMonitor {
		if err := h.stop(); err != nil {
			log.L.WithError(err).Errorf("failed to stop cni network conf monitor for %s", name)
		}
	}
	c.eventMonitor.stop()
	if err := c.streamServer.Stop(); err != nil {
		return fmt.Errorf("failed to stop stream server: %w", err)
	}
	return nil
}

// IsInitialized indicates whether CRI service has finished initialization.
func (c *criService) IsInitialized() bool {
	return c.initialized.Load()
}

func (c *criService) register(s *grpc.Server) error {
	instrumented := instrument.NewService(c)
	runtime.RegisterRuntimeServiceServer(s, instrumented)
	runtime.RegisterImageServiceServer(s, instrumented)
	return nil
}
