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
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/containerd/go-cni"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go/features"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	"k8s.io/kubelet/pkg/cri/streaming"

	apitypes "github.com/containerd/containerd/api/types"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/introspection"
	_ "github.com/containerd/containerd/v2/core/runtime" // for typeurl init
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/config"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/containerd/containerd/v2/internal/cri/nri"
	"github.com/containerd/containerd/v2/internal/cri/server/events"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	"github.com/containerd/containerd/v2/internal/cri/store/label"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	snapshotstore "github.com/containerd/containerd/v2/internal/cri/store/snapshot"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/containerd/v2/internal/eventq"
	nriservice "github.com/containerd/containerd/v2/internal/nri"
	"github.com/containerd/containerd/v2/internal/registrar"
	"github.com/containerd/containerd/v2/pkg/oci"
	osinterface "github.com/containerd/containerd/v2/pkg/os"
	"github.com/containerd/containerd/v2/plugins"
)

var kernelSupportsRRO bool

// defaultNetworkPlugin is used for the default CNI configuration
const defaultNetworkPlugin = "default"

// CRIService is the interface implement CRI remote service server.
type CRIService interface {
	// Closer is used by containerd to gracefully stop cri service.
	io.Closer

	IsInitialized() bool

	Run(ready func()) error
}

type sandboxService interface {
	CreateSandbox(ctx context.Context, info sandbox.Sandbox, opts ...sandbox.CreateOpt) error
	StartSandbox(ctx context.Context, sandboxer string, sandboxID string) (sandbox.ControllerInstance, error)
	WaitSandbox(ctx context.Context, sandboxer string, sandboxID string) (<-chan containerd.ExitStatus, error)
	StopSandbox(ctx context.Context, sandboxer, sandboxID string, opts ...sandbox.StopOpt) error
	ShutdownSandbox(ctx context.Context, sandboxer string, sandboxID string) error
	SandboxStatus(ctx context.Context, sandboxer string, sandboxID string, verbose bool) (sandbox.ControllerStatus, error)
	SandboxPlatform(ctx context.Context, sandboxer string, sandboxID string) (imagespec.Platform, error)
	SandboxController(sandboxer string) (sandbox.Controller, error)
}

// RuntimeService specifies dependencies to runtime service which provides
// the runtime configuration and OCI spec loading.
type RuntimeService interface {
	Config() criconfig.Config

	// LoadCISpec loads cached OCI specs via `Runtime.BaseRuntimeSpec`
	LoadOCISpec(string) (*oci.Spec, error)
}

// ImageService specifies dependencies to image service.
type ImageService interface {
	RuntimeSnapshotter(ctx context.Context, ociRuntime criconfig.Runtime) string

	PullImage(ctx context.Context, name string, credentials func(string) (string, string, error), sandboxConfig *runtime.PodSandboxConfig, runtimeHandler string) (string, error)
	UpdateImage(ctx context.Context, r string) error

	CheckImages(ctx context.Context) error

	GetImage(id string) (imagestore.Image, error)
	GetSnapshot(key, snapshotter string) (snapshotstore.Snapshot, error)

	LocalResolve(refOrID string) (imagestore.Image, error)

	ImageFSPaths() map[string]string
}

// criService implements CRIService.
type criService struct {
	RuntimeService
	ImageService
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
	eventMonitor *events.EventMonitor
	// initialized indicates whether the server is initialized. All GRPC services
	// should return error before the server is initialized.
	initialized atomic.Bool
	// cniNetConfMonitor is used to reload cni network conf if there is
	// any valid fs change events from cni network conf dir.
	cniNetConfMonitor map[string]*cniNetConfSyncer
	// allCaps is the list of the capabilities.
	// When nil, parsed from CapEff of /proc/self/status.
	allCaps []string //nolint:nolintlint,unused // Ignore on non-Linux
	// containerEventsQ is used to capture container events and send them
	// to the callers of GetContainerEvents.
	containerEventsQ eventq.EventQueue[runtime.ContainerEventResponse]
	// nri is used to hook NRI into CRI request processing.
	nri *nri.API
	// sandboxService is the sandbox related service for CRI
	sandboxService sandboxService
	// runtimeHandlers contains runtime handler info
	runtimeHandlers []*runtime.RuntimeHandler
	// runtimeFeatures container runtime features info
	runtimeFeatures *runtime.RuntimeFeatures
}

type CRIServiceOptions struct {
	RuntimeService RuntimeService

	ImageService ImageService

	StreamingConfig streaming.Config

	NRI nriservice.API

	// SandboxControllers is a map of all the loaded sandbox controllers
	SandboxControllers map[string]sandbox.Controller

	// Client is the base containerd client used for accessing services,
	//
	// TODO: Replace this gradually with directly configured instances
	Client *containerd.Client
}

// NewCRIService returns a new instance of CRIService
func NewCRIService(options *CRIServiceOptions) (CRIService, runtime.RuntimeServiceServer, error) {
	ctx := context.Background()
	var err error
	labels := label.NewStore()
	config := options.RuntimeService.Config()

	c := &criService{
		RuntimeService:     options.RuntimeService,
		ImageService:       options.ImageService,
		config:             config,
		client:             options.Client,
		imageFSPaths:       options.ImageService.ImageFSPaths(),
		os:                 osinterface.RealOS{},
		sandboxStore:       sandboxstore.NewStore(labels),
		containerStore:     containerstore.NewStore(labels),
		sandboxNameIndex:   registrar.NewRegistrar(),
		containerNameIndex: registrar.NewRegistrar(),
		netPlugin:          make(map[string]cni.CNI),
		sandboxService:     newCriSandboxService(&config, options.SandboxControllers),
	}

	// TODO: Make discard time configurable
	c.containerEventsQ = eventq.New[runtime.ContainerEventResponse](5*time.Minute, func(event runtime.ContainerEventResponse) {
		containerEventsDroppedCount.Inc()
		log.L.WithFields(
			log.Fields{
				"container": event.ContainerId,
				"type":      event.ContainerEventType,
			}).Warn("container event discarded")
	})

	if err := c.initPlatform(); err != nil {
		return nil, nil, fmt.Errorf("initialize platform: %w", err)
	}

	// prepare streaming server
	c.streamServer, err = streaming.NewServer(options.StreamingConfig, newStreamRuntime(c))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create stream server: %w", err)
	}

	c.eventMonitor = events.NewEventMonitor(&criEventHandler{c: c})

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
				return nil, nil, fmt.Errorf("failed to create cni conf monitor for %s: %w", name, err)
			}
			c.cniNetConfMonitor[name] = m
		}
	}

	c.nri = nri.NewAPI(options.NRI, &criImplementation{c})

	c.runtimeHandlers, err = c.introspectRuntimeHandlers(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to introspect runtime handlers: %w", err)
	}

	c.runtimeFeatures = &runtime.RuntimeFeatures{
		SupplementalGroupsPolicy: true,
	}

	return c, c, nil
}

// Run starts the CRI service.
func (c *criService) Run(ready func()) error {
	log.L.Info("Start subscribing containerd event")
	// note: filters are any match, if you want any match but not in namespace foo
	// then you have to manually filter namespace foo
	c.eventMonitor.Subscribe(c.client, []string{`topic=="/tasks/oom"`, `topic~="/images/"`})

	log.L.Infof("Start recovering state")
	if err := c.recover(ctrdutil.NamespacedContext()); err != nil {
		return fmt.Errorf("failed to recover state: %w", err)
	}

	// Start event handler.
	log.L.Info("Start event monitor")
	eventMonitorErrCh := c.eventMonitor.Start()

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
	if err := c.nri.Register(); err != nil {
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
	c.eventMonitor.Stop()
	if err := c.streamServer.Stop(); err != nil {
		return fmt.Errorf("failed to stop stream server: %w", err)
	}
	return nil
}

// IsInitialized indicates whether CRI service has finished initialization.
func (c *criService) IsInitialized() bool {
	return c.initialized.Load()
}

func (c *criService) introspectRuntimeHandlers(ctx context.Context) ([]*runtime.RuntimeHandler, error) {
	var res []*runtime.RuntimeHandler
	intro := c.client.IntrospectionService()
	for name, r := range c.config.Runtimes {
		h := runtime.RuntimeHandler{
			Name: name,
		}
		rawFeatures, err := introspectRuntimeFeatures(ctx, intro, r)
		if err != nil {
			log.G(ctx).WithError(err).Debugf("failed to introspect features of runtime %q", name)
		} else {
			h.Features = &runtime.RuntimeHandlerFeatures{}
			if slices.Contains(rawFeatures.MountOptions, "rro") {
				if kernelSupportsRRO {
					log.G(ctx).Debugf("runtime %q supports recursive read-only mounts", name)
					h.Features.RecursiveReadOnlyMounts = true
				} else {
					log.G(ctx).Debugf("runtime %q supports recursive read-only mounts, but the kernel does not", name)
				}
			}
			userns := supportsCRIUserns(rawFeatures)
			h.Features.UserNamespaces = userns
			log.G(ctx).Debugf("runtime %q supports CRI userns: %v", name, userns)
		}
		res = append(res, &h)
		if name == c.config.DefaultRuntimeName {
			defH := h
			defH.Name = "" // denotes default
			res = append(res, &defH)
		}
	}
	return res, nil
}

func introspectRuntimeFeatures(ctx context.Context, intro introspection.Service, r config.Runtime) (*features.Features, error) {
	if r.Type != plugins.RuntimeRuncV2 {
		return nil, fmt.Errorf("introspecting OCI runtime features needs the runtime type to be %q, got %q",
			plugins.RuntimeRuncV2, r.Type)
		// For other runtimes, typeurl.MarshalAnyToProto will cause nil panic during typeurl dereference
	}

	rr := &apitypes.RuntimeRequest{
		RuntimePath: r.Type, // "io.containerd.runc.v2"
	}
	if r.Path != "" {
		rr.RuntimePath = r.Path // "/usr/local/bin/crun"
	}
	options, err := config.GenerateRuntimeOptions(r)
	if err != nil {
		return nil, err
	}
	if options != nil {
		rr.Options, err = typeurl.MarshalAnyToProto(options)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal %T: %w", options, err)
		}
	}

	infoResp, err := intro.PluginInfo(ctx, string(plugins.RuntimePluginV2), "task", rr)
	if err != nil {
		return nil, fmt.Errorf("failed to call PluginInfo: %w", err)
	}
	var info apitypes.RuntimeInfo
	if err := typeurl.UnmarshalTo(infoResp.Extra, &info); err != nil {
		return nil, fmt.Errorf("failed to get runtime info from plugin info: %w", err)
	}
	featuresX, err := typeurl.UnmarshalAny(info.Features)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal Features (%T): %w", info.Features, err)
	}
	features, ok := featuresX.(*features.Features)
	if !ok {
		return nil, fmt.Errorf("unknown features type %T", featuresX)
	}
	return features, nil
}

func supportsCRIUserns(f *features.Features) bool {
	if f == nil {
		return false
	}
	userns := slices.Contains(f.Linux.Namespaces, "user")

	var idmap bool
	if m := f.Linux.MountExtensions; m != nil && m.IDMap != nil && m.IDMap.Enabled != nil {
		if *m.IDMap.Enabled {
			idmap = true
		}
	}

	// user namespace support in CRI requires userns and idmap support.
	return userns && idmap
}
