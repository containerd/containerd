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

package stub

import (
	"context"
	"errors"
	"fmt"
	stdnet "net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/containerd/nri/pkg/api"
	nrilog "github.com/containerd/nri/pkg/log"
	"github.com/containerd/nri/pkg/net"
	"github.com/containerd/nri/pkg/net/multiplex"
	"github.com/containerd/ttrpc"
)

// Plugin can implement a number of interfaces related to Pod and Container
// lifecycle events. No any single such inteface is mandatory, therefore the
// Plugin interface itself is empty. Plugins are required to implement at
// least one of these interfaces and this is verified during stub creation.
// Trying to create a stub for a plugin violating this requirement will fail
// with and error.
type Plugin interface{}

// ConfigureInterface handles Configure API request.
type ConfigureInterface interface {
	// Configure the plugin with the given NRI-supplied configuration.
	// If a non-zero EventMask is returned, the plugin will be subscribed
	// to the corresponding.
	Configure(ctx context.Context, config, runtime, version string) (api.EventMask, error)
}

// SynchronizeInterface handles Synchronize API requests.
type SynchronizeInterface interface {
	// Synchronize the state of the plugin with the runtime.
	// The plugin can request updates to containers in response.
	Synchronize(context.Context, []*api.PodSandbox, []*api.Container) ([]*api.ContainerUpdate, error)
}

// ShutdownInterface handles a Shutdown API request.
type ShutdownInterface interface {
	// Shutdown notifies the plugin about the runtime shutting down.
	Shutdown(context.Context)
}

// RunPodInterface handles RunPodSandbox API events.
type RunPodInterface interface {
	// RunPodSandbox relays a RunPodSandbox event to the plugin.
	RunPodSandbox(context.Context, *api.PodSandbox) error
}

// StopPodInterface handles StopPodSandbox API events.
type StopPodInterface interface {
	// StopPodSandbox relays a StopPodSandbox event to the plugin.
	StopPodSandbox(context.Context, *api.PodSandbox) error
}

// RemovePodInterface handles RemovePodSandbox API events.
type RemovePodInterface interface {
	// RemovePodSandbox relays a RemovePodSandbox event to the plugin.
	RemovePodSandbox(context.Context, *api.PodSandbox) error
}

// CreateContainerInterface handles CreateContainer API requests.
type CreateContainerInterface interface {
	// CreateContainer relays a CreateContainer request to the plugin.
	// The plugin can request adjustments to the container being created
	// and updates to other unstopped containers in response.
	CreateContainer(context.Context, *api.PodSandbox, *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error)
}

// StartContainerInterface handles StartContainer API requests.
type StartContainerInterface interface {
	// StartContainer relays a StartContainer event to the plugin.
	StartContainer(context.Context, *api.PodSandbox, *api.Container) error
}

// UpdateContainerInterface handles UpdateContainer API requests.
type UpdateContainerInterface interface {
	// UpdateContainer relays an UpdateContainer request to the plugin.
	// The plugin can request updates both to the container being updated
	// (which then supersedes the original update) and to other unstopped
	// containers in response.
	UpdateContainer(context.Context, *api.PodSandbox, *api.Container, *api.LinuxResources) ([]*api.ContainerUpdate, error)
}

// StopContainerInterface handles StopContainer API requests.
type StopContainerInterface interface {
	// StopContainer relays a StopContainer request to the plugin.
	// The plugin can request updates to unstopped containers in response.
	StopContainer(context.Context, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error)
}

// RemoveContainerInterface handles RemoveContainer API events.
type RemoveContainerInterface interface {
	// RemoveContainer relays a RemoveContainer event to the plugin.
	RemoveContainer(context.Context, *api.PodSandbox, *api.Container) error
}

// PostCreateContainerInterface handles PostCreateContainer API events.
type PostCreateContainerInterface interface {
	// PostCreateContainer relays a PostCreateContainer event to the plugin.
	PostCreateContainer(context.Context, *api.PodSandbox, *api.Container) error
}

// PostStartContainerInterface handles PostStartContainer API events.
type PostStartContainerInterface interface {
	// PostStartContainer relays a PostStartContainer event to the plugin.
	PostStartContainer(context.Context, *api.PodSandbox, *api.Container) error
}

// PostUpdateContainerInterface handles PostUpdateContainer API events.
type PostUpdateContainerInterface interface {
	// PostUpdateContainer relays a PostUpdateContainer event to the plugin.
	PostUpdateContainer(context.Context, *api.PodSandbox, *api.Container) error
}

// Stub is the interface the stub provides for the plugin implementation.
type Stub interface {
	// Run the plugin. Starts the plugin then waits for an error or the plugin to stop
	Run(context.Context) error
	// Start the plugin.
	Start(context.Context) error
	// Stop the plugin.
	Stop()
	// Wait for the plugin to stop.
	Wait()

	// UpdateContainer requests unsolicited updates to containers.
	UpdateContainers([]*api.ContainerUpdate) ([]*api.ContainerUpdate, error)
}

const (
	// Plugin registration timeout.
	registrationTimeout = 2 * time.Second
)

var (
	// Logger for messages generated internally by the stub itself.
	log = nrilog.Get()

	// Used instead of a nil Context in logging.
	noCtx = context.TODO()

	// ErrNoService indicates that the stub has no runtime service/connection,
	// for instance by UpdateContainers on a stub which has not been started.
	ErrNoService = errors.New("stub: no service/connection")
)

// EventMask holds a mask of events for plugin subscription.
type EventMask = api.EventMask

// Option to apply to a plugin during its creation.
type Option func(*stub) error

// WithOnClose sets a notification function to call if the ttRPC connection goes down.
func WithOnClose(onClose func()) Option {
	return func(s *stub) error {
		s.onClose = onClose
		return nil
	}
}

// WithPluginName sets the name to use in plugin registration.
func WithPluginName(name string) Option {
	return func(s *stub) error {
		if s.name != "" {
			return fmt.Errorf("plugin name already set (%q)", s.name)
		}
		s.name = name
		return nil
	}
}

// WithPluginIdx sets the index to use in plugin registration.
func WithPluginIdx(idx string) Option {
	return func(s *stub) error {
		if s.idx != "" {
			return fmt.Errorf("plugin ID already set (%q)", s.idx)
		}
		s.idx = idx
		return nil
	}
}

// WithSocketPath sets the NRI socket path to connect to.
func WithSocketPath(path string) Option {
	return func(s *stub) error {
		s.socketPath = path
		return nil
	}
}

// WithConnection sets an existing NRI connection to use.
func WithConnection(conn stdnet.Conn) Option {
	return func(s *stub) error {
		s.conn = conn
		return nil
	}
}

// WithDialer sets the dialer to use.
func WithDialer(d func(string) (stdnet.Conn, error)) Option {
	return func(s *stub) error {
		s.dialer = d
		return nil
	}
}

// stub implements Stub.
type stub struct {
	sync.Mutex
	plugin     interface{}
	handlers   handlers
	events     api.EventMask
	name       string
	idx        string
	socketPath string
	dialer     func(string) (stdnet.Conn, error)
	conn       stdnet.Conn
	onClose    func()
	rpcm       multiplex.Mux
	rpcl       stdnet.Listener
	rpcs       *ttrpc.Server
	rpcc       *ttrpc.Client
	runtime    api.RuntimeService
	closeOnce  sync.Once
	started    bool
	doneC      chan struct{}
	srvErrC    chan error
	cfgErrC    chan error
}

// Handlers for NRI plugin event and request.
type handlers struct {
	Configure           func(context.Context, string, string, string) (api.EventMask, error)
	Synchronize         func(context.Context, []*api.PodSandbox, []*api.Container) ([]*api.ContainerUpdate, error)
	Shutdown            func(context.Context)
	RunPodSandbox       func(context.Context, *api.PodSandbox) error
	StopPodSandbox      func(context.Context, *api.PodSandbox) error
	RemovePodSandbox    func(context.Context, *api.PodSandbox) error
	CreateContainer     func(context.Context, *api.PodSandbox, *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error)
	StartContainer      func(context.Context, *api.PodSandbox, *api.Container) error
	UpdateContainer     func(context.Context, *api.PodSandbox, *api.Container, *api.LinuxResources) ([]*api.ContainerUpdate, error)
	StopContainer       func(context.Context, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error)
	RemoveContainer     func(context.Context, *api.PodSandbox, *api.Container) error
	PostCreateContainer func(context.Context, *api.PodSandbox, *api.Container) error
	PostStartContainer  func(context.Context, *api.PodSandbox, *api.Container) error
	PostUpdateContainer func(context.Context, *api.PodSandbox, *api.Container) error
}

// New creates a stub with the given plugin and options.
func New(p interface{}, opts ...Option) (Stub, error) {
	stub := &stub{
		plugin:     p,
		name:       os.Getenv(api.PluginNameEnvVar),
		idx:        os.Getenv(api.PluginIdxEnvVar),
		socketPath: api.DefaultSocketPath,
		dialer:     func(p string) (stdnet.Conn, error) { return stdnet.Dial("unix", p) },
		doneC:      make(chan struct{}),
	}

	for _, o := range opts {
		if err := o(stub); err != nil {
			return nil, err
		}
	}

	if err := stub.setupHandlers(); err != nil {
		return nil, err
	}

	if err := stub.ensureIdentity(); err != nil {
		return nil, err
	}

	log.Infof(noCtx, "Created plugin %s (%s, handles %s)", stub.Name(),
		filepath.Base(os.Args[0]), stub.events.PrettyString())

	return stub, nil
}

// Start event processing, register to NRI and wait for getting configured.
func (stub *stub) Start(ctx context.Context) (retErr error) {
	stub.Lock()
	defer stub.Unlock()

	if stub.started {
		return fmt.Errorf("stub already started")
	}
	stub.started = true

	err := stub.connect()
	if err != nil {
		return err
	}

	rpcm := multiplex.Multiplex(stub.conn)
	defer func() {
		if retErr != nil {
			rpcm.Close()
			stub.rpcm = nil
		}
	}()

	rpcl, err := rpcm.Listen(multiplex.PluginServiceConn)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			rpcl.Close()
			stub.rpcl = nil
		}
	}()

	rpcs, err := ttrpc.NewServer()
	if err != nil {
		return fmt.Errorf("failed to create ttrpc server: %w", err)
	}
	defer func() {
		if retErr != nil {
			rpcs.Close()
			stub.rpcs = nil
		}
	}()

	api.RegisterPluginService(rpcs, stub)

	conn, err := rpcm.Open(multiplex.RuntimeServiceConn)
	if err != nil {
		return fmt.Errorf("failed to multiplex ttrpc client connection: %w", err)
	}
	rpcc := ttrpc.NewClient(conn,
		ttrpc.WithOnClose(func() {
			stub.connClosed()
		}),
	)
	defer func() {
		if retErr != nil {
			rpcc.Close()
			stub.rpcc = nil
		}
	}()

	stub.srvErrC = make(chan error, 1)
	stub.cfgErrC = make(chan error, 1)
	go func() {
		stub.srvErrC <- rpcs.Serve(ctx, rpcl)
		close(stub.doneC)
	}()

	stub.rpcm = rpcm
	stub.rpcl = rpcl
	stub.rpcs = rpcs
	stub.rpcc = rpcc

	stub.runtime = api.NewRuntimeClient(rpcc)

	if err = stub.register(ctx); err != nil {
		stub.close()
		return err
	}

	if err = <-stub.cfgErrC; err != nil {
		return err
	}

	log.Infof(ctx, "Started plugin %s...", stub.Name())

	return nil
}

// Stop the plugin.
func (stub *stub) Stop() {
	log.Infof(noCtx, "Stopping plugin %s...", stub.Name())

	stub.Lock()
	defer stub.Unlock()
	stub.close()
}

func (stub *stub) close() {
	stub.closeOnce.Do(func() {
		if stub.rpcl != nil {
			stub.rpcl.Close()
		}
		if stub.rpcs != nil {
			stub.rpcs.Close()
		}
		if stub.rpcc != nil {
			stub.rpcc.Close()
		}
		if stub.rpcm != nil {
			stub.rpcm.Close()
		}
		if stub.srvErrC != nil {
			<-stub.doneC
		}
	})
}

// Run the plugin. Start event processing then wait for an error or getting stopped.
func (stub *stub) Run(ctx context.Context) error {
	var err error

	if err = stub.Start(ctx); err != nil {
		return err
	}

	err = <-stub.srvErrC
	if err == ttrpc.ErrServerClosed {
		return nil
	}

	return err
}

// Wait for the plugin to stop.
func (stub *stub) Wait() {
	stub.Lock()
	if stub.srvErrC == nil {
		return
	}
	stub.Unlock()
	<-stub.doneC
}

// Name returns the full indexed name of the plugin.
func (stub *stub) Name() string {
	return stub.idx + "-" + stub.name
}

// Connect the plugin to NRI.
func (stub *stub) connect() error {
	if stub.conn != nil {
		log.Infof(noCtx, "Using given plugin connection...")
		return nil
	}

	if env := os.Getenv(api.PluginSocketEnvVar); env != "" {
		log.Infof(noCtx, "Using connection %q from environment...", env)

		fd, err := strconv.Atoi(env)
		if err != nil {
			return fmt.Errorf("invalid socket in environment (%s=%q): %w",
				api.PluginSocketEnvVar, env, err)
		}

		stub.conn, err = net.NewFdConn(fd)
		if err != nil {
			return fmt.Errorf("invalid socket (%d) in environment: %w", fd, err)
		}

		return nil
	}

	conn, err := stub.dialer(stub.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to NRI service: %w", err)
	}

	stub.conn = conn

	return nil
}

// Register the plugin with NRI.
func (stub *stub) register(ctx context.Context) error {
	log.Infof(ctx, "Registering plugin %s...", stub.Name())

	ctx, cancel := context.WithTimeout(ctx, registrationTimeout)
	defer cancel()

	req := &api.RegisterPluginRequest{
		PluginName: stub.name,
		PluginIdx:  stub.idx,
	}
	if _, err := stub.runtime.RegisterPlugin(ctx, req); err != nil {
		return fmt.Errorf("failed to register with NRI/Runtime: %w", err)
	}

	return nil
}

// Handle a lost connection.
func (stub *stub) connClosed() {
	stub.close()
	if stub.onClose != nil {
		stub.onClose()
		return
	}

	os.Exit(0)
}

//
// plugin event and request handlers
//

// UpdateContainers requests unsolicited updates to containers.
func (stub *stub) UpdateContainers(update []*api.ContainerUpdate) ([]*api.ContainerUpdate, error) {
	if stub.runtime == nil {
		return nil, ErrNoService
	}

	ctx := context.Background()
	req := &api.UpdateContainersRequest{
		Update: update,
	}
	rpl, err := stub.runtime.UpdateContainers(ctx, req)
	if rpl != nil {
		return rpl.Failed, err
	}
	return nil, err
}

// Configure the plugin.
func (stub *stub) Configure(ctx context.Context, req *api.ConfigureRequest) (rpl *api.ConfigureResponse, retErr error) {
	var (
		events api.EventMask
		err    error
	)

	log.Infof(ctx, "Configuring plugin %s for runtime %s/%s...", stub.Name(),
		req.RuntimeName, req.RuntimeVersion)

	defer func() {
		stub.cfgErrC <- retErr
	}()

	if handler := stub.handlers.Configure; handler == nil {
		events = stub.events
	} else {
		events, err = handler(ctx, req.Config, req.RuntimeName, req.RuntimeVersion)
		if err != nil {
			log.Errorf(ctx, "Plugin configuration failed: %v", err)
			return nil, err
		}

		if events == 0 {
			events = stub.events
		}

		// Only allow plugins to subscribe to events they can handle.
		if extra := events & ^stub.events; extra != 0 {
			log.Errorf(ctx, "Plugin subscribed for unhandled events %s (0x%x)",
				extra.PrettyString(), extra)
			return nil, fmt.Errorf("internal error: unhandled events %s (0x%x)",
				extra.PrettyString(), extra)
		}

		log.Infof(ctx, "Subscribing plugin %s (%s) for events %s", stub.Name(),
			filepath.Base(os.Args[0]), events.PrettyString())
	}

	return &api.ConfigureResponse{
		Events: int32(events),
	}, nil
}

// Synchronize the state of the plugin with the runtime.
func (stub *stub) Synchronize(ctx context.Context, req *api.SynchronizeRequest) (*api.SynchronizeResponse, error) {
	handler := stub.handlers.Synchronize
	if handler == nil {
		return &api.SynchronizeResponse{}, nil
	}
	update, err := handler(ctx, req.Pods, req.Containers)
	return &api.SynchronizeResponse{
		Update: update,
	}, err
}

// Shutdown the plugin.
func (stub *stub) Shutdown(ctx context.Context, req *api.ShutdownRequest) (*api.ShutdownResponse, error) {
	handler := stub.handlers.Shutdown
	if handler != nil {
		handler(ctx)
	}
	return &api.ShutdownResponse{}, nil
}

// CreateContainer request handler.
func (stub *stub) CreateContainer(ctx context.Context, req *api.CreateContainerRequest) (*api.CreateContainerResponse, error) {
	handler := stub.handlers.CreateContainer
	if handler == nil {
		return nil, nil
	}
	adjust, update, err := handler(ctx, req.Pod, req.Container)
	return &api.CreateContainerResponse{
		Adjust: adjust,
		Update: update,
	}, err
}

// UpdateContainer request handler.
func (stub *stub) UpdateContainer(ctx context.Context, req *api.UpdateContainerRequest) (*api.UpdateContainerResponse, error) {
	handler := stub.handlers.UpdateContainer
	if handler == nil {
		return nil, nil
	}
	update, err := handler(ctx, req.Pod, req.Container, req.LinuxResources)
	return &api.UpdateContainerResponse{
		Update: update,
	}, err
}

// StopContainer request handler.
func (stub *stub) StopContainer(ctx context.Context, req *api.StopContainerRequest) (*api.StopContainerResponse, error) {
	handler := stub.handlers.StopContainer
	if handler == nil {
		return nil, nil
	}
	update, err := handler(ctx, req.Pod, req.Container)
	return &api.StopContainerResponse{
		Update: update,
	}, err
}

// StateChange event handler.
func (stub *stub) StateChange(ctx context.Context, evt *api.StateChangeEvent) (*api.Empty, error) {
	var err error
	switch evt.Event {
	case api.Event_RUN_POD_SANDBOX:
		if handler := stub.handlers.RunPodSandbox; handler != nil {
			err = handler(ctx, evt.Pod)
		}
	case api.Event_STOP_POD_SANDBOX:
		if handler := stub.handlers.StopPodSandbox; handler != nil {
			err = handler(ctx, evt.Pod)
		}
	case api.Event_REMOVE_POD_SANDBOX:
		if handler := stub.handlers.RemovePodSandbox; handler != nil {
			err = handler(ctx, evt.Pod)
		}
	case api.Event_POST_CREATE_CONTAINER:
		if handler := stub.handlers.PostCreateContainer; handler != nil {
			err = handler(ctx, evt.Pod, evt.Container)
		}
	case api.Event_START_CONTAINER:
		if handler := stub.handlers.StartContainer; handler != nil {
			err = handler(ctx, evt.Pod, evt.Container)
		}
	case api.Event_POST_START_CONTAINER:
		if handler := stub.handlers.PostStartContainer; handler != nil {
			err = handler(ctx, evt.Pod, evt.Container)
		}
	case api.Event_POST_UPDATE_CONTAINER:
		if handler := stub.handlers.PostUpdateContainer; handler != nil {
			err = handler(ctx, evt.Pod, evt.Container)
		}
	case api.Event_REMOVE_CONTAINER:
		if handler := stub.handlers.RemoveContainer; handler != nil {
			err = handler(ctx, evt.Pod, evt.Container)
		}
	}

	return &api.StateChangeResponse{}, err
}

// ensureIdentity sets plugin index and name from the binary if those are unset.
func (stub *stub) ensureIdentity() error {
	if stub.idx != "" && stub.name != "" {
		return nil
	}

	if stub.idx != "" {
		stub.name = filepath.Base(os.Args[0])
		return nil
	}

	idx, name, err := api.ParsePluginName(filepath.Base(os.Args[0]))
	if err != nil {
		return err
	}

	stub.name = name
	stub.idx = idx

	return nil
}

// Set up event handlers and the subscription mask for the plugin.
func (stub *stub) setupHandlers() error {
	if plugin, ok := stub.plugin.(ConfigureInterface); ok {
		stub.handlers.Configure = plugin.Configure
	}
	if plugin, ok := stub.plugin.(SynchronizeInterface); ok {
		stub.handlers.Synchronize = plugin.Synchronize
	}
	if plugin, ok := stub.plugin.(ShutdownInterface); ok {
		stub.handlers.Shutdown = plugin.Shutdown
	}

	if plugin, ok := stub.plugin.(RunPodInterface); ok {
		stub.handlers.RunPodSandbox = plugin.RunPodSandbox
		stub.events.Set(api.Event_RUN_POD_SANDBOX)
	}
	if plugin, ok := stub.plugin.(StopPodInterface); ok {
		stub.handlers.StopPodSandbox = plugin.StopPodSandbox
		stub.events.Set(api.Event_STOP_POD_SANDBOX)
	}
	if plugin, ok := stub.plugin.(RemovePodInterface); ok {
		stub.handlers.RemovePodSandbox = plugin.RemovePodSandbox
		stub.events.Set(api.Event_REMOVE_POD_SANDBOX)
	}
	if plugin, ok := stub.plugin.(CreateContainerInterface); ok {
		stub.handlers.CreateContainer = plugin.CreateContainer
		stub.events.Set(api.Event_CREATE_CONTAINER)
	}
	if plugin, ok := stub.plugin.(StartContainerInterface); ok {
		stub.handlers.StartContainer = plugin.StartContainer
		stub.events.Set(api.Event_START_CONTAINER)
	}
	if plugin, ok := stub.plugin.(UpdateContainerInterface); ok {
		stub.handlers.UpdateContainer = plugin.UpdateContainer
		stub.events.Set(api.Event_UPDATE_CONTAINER)
	}
	if plugin, ok := stub.plugin.(StopContainerInterface); ok {
		stub.handlers.StopContainer = plugin.StopContainer
		stub.events.Set(api.Event_STOP_CONTAINER)
	}
	if plugin, ok := stub.plugin.(RemoveContainerInterface); ok {
		stub.handlers.RemoveContainer = plugin.RemoveContainer
		stub.events.Set(api.Event_REMOVE_CONTAINER)
	}
	if plugin, ok := stub.plugin.(PostCreateContainerInterface); ok {
		stub.handlers.PostCreateContainer = plugin.PostCreateContainer
		stub.events.Set(api.Event_POST_CREATE_CONTAINER)
	}
	if plugin, ok := stub.plugin.(PostStartContainerInterface); ok {
		stub.handlers.PostStartContainer = plugin.PostStartContainer
		stub.events.Set(api.Event_POST_START_CONTAINER)
	}
	if plugin, ok := stub.plugin.(PostUpdateContainerInterface); ok {
		stub.handlers.PostUpdateContainer = plugin.PostUpdateContainer
		stub.events.Set(api.Event_POST_UPDATE_CONTAINER)
	}

	if stub.events == 0 {
		return fmt.Errorf("internal error: plugin %T does not implement any NRI request handlers",
			stub.plugin)
	}

	return nil
}
