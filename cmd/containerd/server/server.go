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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/containerd/log"
	"github.com/docker/go-metrics"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"

	diffapi "github.com/containerd/containerd/api/services/diff/v1"
	sbapi "github.com/containerd/containerd/api/services/sandbox/v1"
	ssapi "github.com/containerd/containerd/api/services/snapshots/v1"
	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	srvconfig "github.com/containerd/containerd/v2/cmd/containerd/server/config"
	csproxy "github.com/containerd/containerd/v2/core/content/proxy"
	"github.com/containerd/containerd/v2/core/diff"
	diffproxy "github.com/containerd/containerd/v2/core/diff/proxy"
	sbproxy "github.com/containerd/containerd/v2/core/sandbox/proxy"
	ssproxy "github.com/containerd/containerd/v2/core/snapshots/proxy"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/dialer"
	"github.com/containerd/containerd/v2/pkg/sys"
	"github.com/containerd/containerd/v2/pkg/timeout"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/services/warning"
)

// CreateTopLevelDirectories creates the top-level root and state directories.
func CreateTopLevelDirectories(config *srvconfig.Config) error {
	switch {
	case config.Root == "":
		return errors.New("root must be specified")
	case config.State == "":
		return errors.New("state must be specified")
	case config.Root == config.State:
		return errors.New("root and state must be different paths")
	}

	if err := sys.MkdirAllWithACL(config.Root, 0o700); err != nil {
		return err
	}
	// chmod is needed for upgrading from an older release that created the dir with 0o711
	if err := os.Chmod(config.Root, 0o700); err != nil {
		return err
	}

	// For supporting userns-remapped containers, the state dir cannot be just mkdired with 0o700.
	// Each of plugins creates a dedicated directory beneath the state dir with appropriate permission bits.
	if err := sys.MkdirAllWithACL(config.State, 0o711); err != nil {
		return err
	}
	if config.State != defaults.DefaultStateDir {
		// XXX: socketRoot in pkg/shim is hard-coded to the default state directory.
		// See https://github.com/containerd/containerd/issues/10502#issuecomment-2249268582 for why it's set up that way.
		// The default fifo directory in pkg/cio is also configured separately and defaults to the default state directory instead of the configured state directory.
		// Make sure the default state directory is created with the correct permissions.
		if err := sys.MkdirAllWithACL(defaults.DefaultStateDir, 0o711); err != nil {
			return err
		}
	}

	if config.TempDir != "" {
		if err := sys.MkdirAllWithACL(config.TempDir, 0o700); err != nil {
			return err
		}
		// chmod is needed for upgrading from an older release that created the dir with 0o711
		if err := os.Chmod(config.Root, 0o700); err != nil {
			return err
		}
		if runtime.GOOS == "windows" {
			// On Windows, the Host Compute Service (vmcompute) will read the
			// TEMP/TMP setting from the calling process when creating the
			// tempdir to extract an image layer to. This allows the
			// administrator to align the tempdir location with the same volume
			// as the snapshot dir to avoid a copy operation when moving the
			// extracted layer to the snapshot dir location.
			os.Setenv("TEMP", config.TempDir)
			os.Setenv("TMP", config.TempDir)
		} else {
			os.Setenv("TMPDIR", config.TempDir)
		}
	}
	return nil
}

// New creates and initializes a new containerd server
func New(ctx context.Context, config *srvconfig.Config) (*Server, error) {
	if err := apply(ctx, config); err != nil {
		return nil, err
	}
	for key, sec := range config.Timeouts {
		d, err := time.ParseDuration(sec)
		if err != nil {
			return nil, fmt.Errorf("unable to parse %s into a time duration", sec)
		}
		timeout.Set(key, d)
	}
	loaded, err := LoadPlugins(ctx, config)
	if err != nil {
		return nil, err
	}
	for id, p := range config.StreamProcessors {
		diff.RegisterProcessor(diff.BinaryHandler(id, p.Returns, p.Accepts, p.Path, p.Args, p.Env))
	}
	var (
		s = &Server{
			config: config,
		}
		initialized  = plugin.NewPluginSet()
		required     = make(map[string]struct{})
		grpcAddress  = readString(config.Plugins, "io.containerd.server.v1.grpc", "address")
		ttrpcAddress = readString(config.Plugins, "io.containerd.server.v1.ttrpc", "address")
	)
	for _, r := range config.RequiredPlugins {
		required[r] = struct{}{}
	}

	for _, p := range loaded {
		id := p.URI()
		log.G(ctx).WithFields(log.Fields{"id": id, "type": p.Type}).Info("loading plugin")
		var mustSucceed atomic.Int32

		initContext := plugin.NewContext(
			ctx,
			initialized,
			map[string]string{
				plugins.PropertyRootDir:      filepath.Join(config.Root, id),
				plugins.PropertyStateDir:     filepath.Join(config.State, id),
				plugins.PropertyGRPCAddress:  grpcAddress,
				plugins.PropertyTTRPCAddress: ttrpcAddress,
			},
		)
		initContext.RegisterReadiness = func() func() {
			mustSucceed.Store(1)
			return s.RegisterReadiness()
		}

		// load the plugin specific configuration if it is provided
		if p.Config != nil {
			pc, err := config.Decode(ctx, id, p.Config)
			if err != nil {
				return nil, err
			}
			initContext.Config = pc
		}
		result := p.Init(initContext)
		if err := initialized.Add(result); err != nil {
			return nil, fmt.Errorf("could not add plugin result to plugin set: %w", err)
		}

		instance, err := result.Instance()
		if err != nil {
			if plugin.IsSkipPlugin(err) {
				log.G(ctx).WithFields(log.Fields{"error": err, "id": id, "type": p.Type}).Info("skip loading plugin")
			} else {
				log.G(ctx).WithFields(log.Fields{"error": err, "id": id, "type": p.Type}).Warn("failed to load plugin")
			}
			if _, ok := required[id]; ok {
				return nil, fmt.Errorf("load required plugin %s: %w", id, err)
			}
			// If readiness was registered during initialization, the plugin cannot fail
			if mustSucceed.Load() != 0 {
				return nil, fmt.Errorf("plugin failed after registering readiness %s: %w", id, err)
			}
			continue
		}

		delete(required, id)
		if p.Type == plugins.ServerPlugin {
			srv, ok := instance.(server)
			if !ok {
				log.G(ctx).WithField("id", id).Warn("plugin does not implement server interface, will not be started")
			}
			s.servers = append(s.servers, srv)
		}

		s.plugins = append(s.plugins, result)
	}
	if len(required) != 0 {
		var missing []string
		for id := range required {
			missing = append(missing, id)
		}
		return nil, fmt.Errorf("required plugin %s not included", missing)
	}

	recordConfigDeprecations(ctx, config, initialized)
	return s, nil
}

// recordConfigDeprecations attempts to record use of any deprecated config field.  Failures are logged and ignored.
func recordConfigDeprecations(ctx context.Context, config *srvconfig.Config, set *plugin.Set) {
	// record any detected deprecations without blocking server startup
	p := set.Get(plugins.WarningPlugin, plugins.DeprecationsPlugin)
	if p == nil {
		log.G(ctx).Warn("failed to find warning service to record deprecations")
		return
	}
	instance, err := p.Instance()
	if err != nil {
		log.G(ctx).WithError(err).Warn("failed to load warning service to record deprecations")
		return
	}
	warn, ok := instance.(warning.Service)
	if !ok {
		log.G(ctx).WithError(err).Warn("failed to load warning service to record deprecations, unexpected plugin type")
		return
	}

	// warn.Emit(ctx, deprecation...) will be used for future deprecations
	_ = warn
}

type server interface {
	Start(context.Context) error
}

// Server is the containerd main daemon
type Server struct {
	servers []server
	config  *srvconfig.Config
	plugins []*plugin.Plugin
	ready   sync.WaitGroup
}

// ServeMetrics provides a prometheus endpoint for exposing metrics
func (s *Server) ServeMetrics(l net.Listener) error {
	m := http.NewServeMux()
	m.Handle("/v1/metrics", metrics.Handler())
	srv := &http.Server{
		Handler:           m,
		ReadHeaderTimeout: 5 * time.Minute, // "G112: Potential Slowloris Attack (gosec)"; not a real concern for our use, so setting a long timeout.
	}
	return trapClosedConnErr(srv.Serve(l))
}

// Start the services, this will normally start listening on sockets
// and serving requests.
func (s *Server) Start(ctx context.Context) error {
	for _, srv := range s.servers {
		if err := srv.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Stop the containerd server canceling any open connections
func (s *Server) Stop() {
	for i := len(s.plugins) - 1; i >= 0; i-- {
		p := s.plugins[i]
		instance, err := p.Instance()
		if err != nil {
			log.L.WithFields(log.Fields{"error": err, "id": p.Registration.URI()}).Error("could not get plugin instance")
			continue
		}
		closer, ok := instance.(io.Closer)
		if !ok {
			continue
		}
		if err := closer.Close(); err != nil {
			log.L.WithFields(log.Fields{"error": err, "id": p.Registration.URI()}).Error("failed to close plugin")
		}
	}
}

func (s *Server) RegisterReadiness() func() {
	s.ready.Add(1)
	return func() {
		s.ready.Done()
	}
}

func (s *Server) Wait() {
	s.ready.Wait()
}

// LoadPlugins loads all plugins into containerd and generates an ordered graph
// of all plugins.
func LoadPlugins(ctx context.Context, config *srvconfig.Config) ([]plugin.Registration, error) {
	// load all plugins into containerd
	clients := &proxyClients{}
	for name, pp := range config.ProxyPlugins {
		var (
			t plugin.Type
			f func(*grpc.ClientConn) interface{}

			address = pp.Address
			p       v1.Platform
			err     error
		)

		switch pp.Type {
		case string(plugins.SnapshotPlugin), "snapshot":
			t = plugins.SnapshotPlugin
			ssname := name
			f = func(conn *grpc.ClientConn) interface{} {
				return ssproxy.NewSnapshotter(ssapi.NewSnapshotsClient(conn), ssname)
			}

		case string(plugins.ContentPlugin), "content":
			t = plugins.ContentPlugin
			f = func(conn *grpc.ClientConn) interface{} {
				return csproxy.NewContentStore(conn)
			}
		case string(plugins.SandboxControllerPlugin), "sandbox":
			t = plugins.SandboxControllerPlugin
			f = func(conn *grpc.ClientConn) interface{} {
				return sbproxy.NewSandboxController(sbapi.NewControllerClient(conn), name)
			}
		case string(plugins.DiffPlugin), "diff":
			t = plugins.DiffPlugin
			f = func(conn *grpc.ClientConn) interface{} {
				return diffproxy.NewDiffApplier(diffapi.NewDiffClient(conn))
			}
		default:
			log.G(ctx).WithField("type", pp.Type).Warn("unknown proxy plugin type")
		}
		if pp.Platform != "" {
			p, err = platforms.Parse(pp.Platform)
			if err != nil {
				log.G(ctx).WithFields(log.Fields{"error": err, "plugin": name}).Warn("skipping proxy platform with bad platform")
			}
		} else {
			p = platforms.DefaultSpec()
		}

		exports := pp.Exports
		if exports == nil {
			exports = map[string]string{}
		}
		exports["address"] = address

		registry.Register(&plugin.Registration{
			Type: t,
			ID:   name,
			InitFn: func(ic *plugin.InitContext) (interface{}, error) {
				ic.Meta.Exports = exports
				ic.Meta.Platforms = append(ic.Meta.Platforms, p)
				ic.Meta.Capabilities = pp.Capabilities
				conn, err := clients.getClient(address)
				if err != nil {
					return nil, err
				}
				return f(conn), nil
			},
		})

	}

	filter := srvconfig.V2DisabledFilter
	// return the ordered graph for plugins
	return registry.Graph(filter(config.DisabledPlugins)), nil
}

type proxyClients struct {
	m       sync.Mutex
	clients map[string]*grpc.ClientConn
}

func (pc *proxyClients) getClient(address string) (*grpc.ClientConn, error) {
	pc.m.Lock()
	defer pc.m.Unlock()
	if pc.clients == nil {
		pc.clients = map[string]*grpc.ClientConn{}
	} else if c, ok := pc.clients[address]; ok {
		return c, nil
	}

	backoffConfig := backoff.DefaultConfig
	backoffConfig.MaxDelay = 3 * time.Second
	connParams := grpc.ConnectParams{
		Backoff: backoffConfig,
	}
	gopts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(connParams),
		grpc.WithContextDialer(dialer.ContextDialer),

		// TODO(stevvooe): We may need to allow configuration of this on the client.
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(defaults.DefaultMaxRecvMsgSize)),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(defaults.DefaultMaxSendMsgSize)),
	}

	conn, err := grpc.NewClient(dialer.DialAddress(address), gopts...)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %q: %w", address, err)
	}

	pc.clients[address] = conn

	return conn, nil
}

func readString(properties map[string]any, key ...string) string {
	for _, k := range key {
		if v, ok := properties[k]; ok {
			switch v := v.(type) {
			case map[string]any:
				properties = v
				continue
			case string:
				return v
			case fmt.Stringer:
				return v.String()
			case []byte:
				return string(v)
			}
		}
		break
	}
	return ""
}

func trapClosedConnErr(err error) error {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
