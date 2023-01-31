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

package adaptation

import (
	"context"
	"errors"
	"fmt"
	stdnet "net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/log"
	"github.com/containerd/nri/pkg/net"
	"github.com/containerd/nri/pkg/net/multiplex"
	"github.com/containerd/ttrpc"
)

const (
	pluginRegistrationTimeout = 2 * time.Second
	pluginRequestTimeout      = 2 * time.Second
)

type plugin struct {
	sync.Mutex
	idx    string
	base   string
	cfg    string
	pid    int
	cmd    *exec.Cmd
	mux    multiplex.Mux
	rpcc   *ttrpc.Client
	rpcl   stdnet.Listener
	rpcs   *ttrpc.Server
	events EventMask
	closed bool
	stub   api.PluginService
	regC   chan error
	closeC chan struct{}
	r      *Adaptation
}

// Launch a pre-installed plugin with a pre-connected socketpair.
func (r *Adaptation) newLaunchedPlugin(dir, idx, base, cfg string) (p *plugin, retErr error) {
	name := idx + "-" + base

	sockets, err := net.NewSocketPair()
	if err != nil {
		return nil, fmt.Errorf("failed to create plugin connection for plugin %q: %w", name, err)
	}
	defer sockets.Close()

	conn, err := sockets.LocalConn()
	if err != nil {
		return nil, fmt.Errorf("failed to set up local connection for plugin %q: %w", name, err)
	}

	peerFile := sockets.PeerFile()
	defer func() {
		peerFile.Close()
		if retErr != nil {
			conn.Close()
		}
	}()

	cmd := exec.Command(filepath.Join(dir, name))
	cmd.ExtraFiles = []*os.File{peerFile}
	cmd.Env = []string{
		api.PluginNameEnvVar + "=" + name,
		api.PluginIdxEnvVar + "=" + idx,
		api.PluginSocketEnvVar + "=3",
	}

	p = &plugin{
		cfg:    cfg,
		cmd:    cmd,
		idx:    idx,
		base:   base,
		regC:   make(chan error, 1),
		closeC: make(chan struct{}),
		r:      r,
	}

	if err = p.cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed launch plugin %q: %w", p.name(), err)
	}

	if err = p.connect(conn); err != nil {
		return nil, err
	}

	return p, nil
}

// Create a plugin (stub) for an accepted external plugin connection.
func (r *Adaptation) newExternalPlugin(conn stdnet.Conn) (p *plugin, retErr error) {
	p = &plugin{
		regC:   make(chan error, 1),
		closeC: make(chan struct{}),
		r:      r,
	}
	if err := p.connect(conn); err != nil {
		return nil, err
	}

	return p, nil
}

// Check if the plugin is external (was not launched by us).
func (p *plugin) isExternal() bool {
	return p.cmd == nil
}

// 'connect' a plugin, setting up multiplexing on its socket.
func (p *plugin) connect(conn stdnet.Conn) (retErr error) {
	mux := multiplex.Multiplex(conn, multiplex.WithBlockedRead())
	defer func() {
		if retErr != nil {
			mux.Close()
		}
	}()

	pconn, err := mux.Open(multiplex.PluginServiceConn)
	if err != nil {
		return fmt.Errorf("failed to mux plugin connection for plugin %q: %w", p.name(), err)
	}
	rpcc := ttrpc.NewClient(pconn, ttrpc.WithOnClose(
		func() {
			log.Infof(noCtx, "connection to plugin %q closed", p.name())
			close(p.closeC)
			p.close()
		}))
	defer func() {
		if retErr != nil {
			rpcc.Close()
		}
	}()
	stub := api.NewPluginClient(rpcc)

	rpcs, err := ttrpc.NewServer()
	if err != nil {
		return fmt.Errorf("failed to create ttrpc server for plugin %q: %w", p.name(), err)
	}
	defer func() {
		if retErr != nil {
			rpcs.Close()
		}
	}()

	rpcl, err := mux.Listen(multiplex.RuntimeServiceConn)
	if err != nil {
		return fmt.Errorf("failed to create mux runtime listener for plugin %q: %w", p.name(), err)
	}

	p.mux = mux
	p.rpcc = rpcc
	p.rpcl = rpcl
	p.rpcs = rpcs
	p.stub = stub

	p.pid, err = getPeerPid(p.mux.Trunk())
	if err != nil {
		log.Warnf(noCtx, "failed to determine plugin pid pid: %v", err)
	}

	api.RegisterRuntimeService(p.rpcs, p)

	return nil
}

// Start Runtime service, wait for plugin to register, then configure it.
func (p *plugin) start(name, version string) error {
	var err error

	go func() {
		err := p.rpcs.Serve(context.Background(), p.rpcl)
		if err != ttrpc.ErrServerClosed {
			log.Infof(noCtx, "ttrpc server for plugin %q closed (%v)", p.name(), err)
		}
		p.close()
	}()

	p.mux.Unblock()

	select {
	case err = <-p.regC:
		if err != nil {
			return fmt.Errorf("failed to register plugin: %w", err)
		}
	case <-p.closeC:
		return fmt.Errorf("failed to register plugin, connection closed")
	case <-time.After(pluginRegistrationTimeout):
		p.close()
		p.stop()
		return errors.New("plugin registration timed out")
	}

	err = p.configure(context.Background(), name, version, p.cfg)
	if err != nil {
		p.close()
		p.stop()
		return err
	}

	return nil
}

// close a plugin shutting down its multiplexed ttrpc connections.
func (p *plugin) close() {
	p.Lock()
	defer p.Unlock()
	if p.closed {
		return
	}

	p.closed = true
	p.mux.Close()
	p.rpcc.Close()
	p.rpcs.Close()
	p.rpcl.Close()
}

func (p *plugin) isClosed() bool {
	p.Lock()
	defer p.Unlock()
	return p.closed
}

// stop a plugin (if it was launched by us)
func (p *plugin) stop() error {
	if p.isExternal() || p.cmd.Process == nil {
		return nil
	}

	// TODO(klihub):
	//   We should attempt a graceful shutdown of the process here...
	//     - send it SIGINT
	//     - give the it some slack waiting with a timeout
	//     - butcher it with SIGKILL after the timeout

	p.cmd.Process.Kill()
	p.cmd.Process.Wait()
	p.cmd.Process.Release()

	return nil
}

// Name returns a string indentication for the plugin.
func (p *plugin) name() string {
	return p.idx + "-" + p.base
}

func (p *plugin) qualifiedName() string {
	var kind, idx, base string
	if p.isExternal() {
		kind = "external"
	} else {
		kind = "pre-connected"
	}
	if idx = p.idx; idx == "" {
		idx = "??"
	}
	if base = p.base; base == "" {
		base = "plugin"
	}
	return kind + ":" + idx + "-" + base + "[" + strconv.Itoa(p.pid) + "]"
}

// RegisterPlugin handles the plugin's registration request.
func (p *plugin) RegisterPlugin(ctx context.Context, req *RegisterPluginRequest) (*RegisterPluginResponse, error) {
	if p.isExternal() {
		if req.PluginName == "" {
			p.regC <- fmt.Errorf("plugin %q registered empty name", p.qualifiedName())
			return &RegisterPluginResponse{}, errors.New("invalid (empty) plugin name")
		}
		if err := api.CheckPluginIndex(req.PluginIdx); err != nil {
			p.regC <- fmt.Errorf("plugin %q registered invalid index: %w", req.PluginName, err)
			return &RegisterPluginResponse{}, fmt.Errorf("invalid plugin index: %w", err)
		}
		p.base = req.PluginName
		p.idx = req.PluginIdx
	}

	log.Infof(ctx, "plugin %q registered as %q", p.qualifiedName(), p.name())

	p.regC <- nil
	return &RegisterPluginResponse{}, nil
}

// UpdateContainers relays container update request to the runtime.
func (p *plugin) UpdateContainers(ctx context.Context, req *UpdateContainersRequest) (*UpdateContainersResponse, error) {
	log.Infof(ctx, "plugin %q requested container updates", p.name())

	failed, err := p.r.updateContainers(ctx, req.Update)
	return &UpdateContainersResponse{
		Failed: failed,
	}, err
}

// configure the plugin and subscribe it for the events it requested.
func (p *plugin) configure(ctx context.Context, name, version, config string) error {
	ctx, cancel := context.WithTimeout(ctx, pluginRequestTimeout)
	defer cancel()

	rpl, err := p.stub.Configure(ctx, &ConfigureRequest{
		Config:         config,
		RuntimeName:    name,
		RuntimeVersion: version,
	})
	if err != nil {
		return fmt.Errorf("failed to configure plugin: %w", err)
	}

	events := EventMask(rpl.Events)
	if events != 0 {
		if extra := events &^ ValidEvents; extra != 0 {
			return fmt.Errorf("invalid plugin events: 0x%x", extra)
		}
	} else {
		events = ValidEvents
	}
	p.events = events

	return nil
}

// synchronize the plugin with the current state of the runtime.
func (p *plugin) synchronize(ctx context.Context, pods []*PodSandbox, containers []*Container) ([]*ContainerUpdate, error) {
	log.Infof(ctx, "synchronizing plugin %s", p.name())

	ctx, cancel := context.WithTimeout(ctx, pluginRequestTimeout)
	defer cancel()

	req := &SynchronizeRequest{
		Pods:       pods,
		Containers: containers,
	}
	rpl, err := p.stub.Synchronize(ctx, req)
	if err != nil {
		return nil, err
	}

	return rpl.Update, nil
}

// Relay CreateContainer request to plugin.
func (p *plugin) createContainer(ctx context.Context, req *CreateContainerRequest) (*CreateContainerResponse, error) {
	if !p.events.IsSet(Event_CREATE_CONTAINER) {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, pluginRequestTimeout)
	defer cancel()

	rpl, err := p.stub.CreateContainer(ctx, req)
	if err != nil {
		if isFatalError(err) {
			log.Errorf(ctx, "closing plugin %s, failed to handle CreateContainer request: %v",
				p.name(), err)
			p.close()
			return nil, nil
		}
		return nil, err
	}

	return rpl, nil
}

// Relay UpdateContainer request to plugin.
func (p *plugin) updateContainer(ctx context.Context, req *UpdateContainerRequest) (*UpdateContainerResponse, error) {
	if !p.events.IsSet(Event_UPDATE_CONTAINER) {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, pluginRequestTimeout)
	defer cancel()

	rpl, err := p.stub.UpdateContainer(ctx, req)
	if err != nil {
		if isFatalError(err) {
			log.Errorf(ctx, "closing plugin %s, failed to handle UpdateContainer request: %v",
				p.name(), err)
			p.close()
			return nil, nil
		}
		return nil, err
	}

	return rpl, nil
}

// Relay StopContainer request to the plugin.
func (p *plugin) stopContainer(ctx context.Context, req *StopContainerRequest) (*StopContainerResponse, error) {
	if !p.events.IsSet(Event_STOP_CONTAINER) {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, pluginRequestTimeout)
	defer cancel()

	rpl, err := p.stub.StopContainer(ctx, req)
	if err != nil {
		if isFatalError(err) {
			log.Errorf(ctx, "closing plugin %s, failed to handle StopContainer request: %v",
				p.name(), err)
			p.close()
			return nil, nil
		}
		return nil, err
	}

	return rpl, nil
}

// Relay other pod or container state change events to the plugin.
func (p *plugin) StateChange(ctx context.Context, evt *StateChangeEvent) error {
	if !p.events.IsSet(evt.Event) {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, pluginRequestTimeout)
	defer cancel()

	_, err := p.stub.StateChange(ctx, evt)
	if err != nil {
		if isFatalError(err) {
			log.Errorf(ctx, "closing plugin %s, failed to handle event %d: %v",
				p.name(), evt.Event, err)
			p.close()
			return nil
		}
		return err
	}

	return nil
}

// isFatalError returns true if the error is fatal and the plugin connection shoudld be closed.
func isFatalError(err error) bool {
	switch {
	case errors.Is(err, ttrpc.ErrClosed):
		return true
	case errors.Is(err, ttrpc.ErrServerClosed):
		return true
	case errors.Is(err, ttrpc.ErrProtocol):
		return true
	case errors.Is(err, context.DeadlineExceeded):
		return true
	}
	return false
}
