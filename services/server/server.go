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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"expvar"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/ttrpc"
	"github.com/docker/go-metrics"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	csapi "github.com/containerd/containerd/v2/api/services/content/v1"
	diffapi "github.com/containerd/containerd/v2/api/services/diff/v1"
	sbapi "github.com/containerd/containerd/v2/api/services/sandbox/v1"
	ssapi "github.com/containerd/containerd/v2/api/services/snapshots/v1"
	"github.com/containerd/containerd/v2/content/local"
	csproxy "github.com/containerd/containerd/v2/content/proxy"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/diff"
	diffproxy "github.com/containerd/containerd/v2/diff/proxy"
	"github.com/containerd/containerd/v2/pkg/deprecation"
	"github.com/containerd/containerd/v2/pkg/dialer"
	"github.com/containerd/containerd/v2/pkg/timeout"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins"
	sbproxy "github.com/containerd/containerd/v2/sandbox/proxy"
	srvconfig "github.com/containerd/containerd/v2/services/server/config"
	"github.com/containerd/containerd/v2/services/warning"
	ssproxy "github.com/containerd/containerd/v2/snapshots/proxy"
	"github.com/containerd/containerd/v2/sys"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/dynamic"
	"github.com/containerd/plugin/registry"
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

	if err := sys.MkdirAllWithACL(config.Root, 0o711); err != nil {
		return err
	}

	if err := sys.MkdirAllWithACL(config.State, 0o711); err != nil {
		return err
	}

	if config.TempDir != "" {
		if err := sys.MkdirAllWithACL(config.TempDir, 0o711); err != nil {
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
	var (
		version    = config.Version
		migrationT time.Duration
	)
	if version < srvconfig.CurrentConfigVersion {
		// Migrate config to latest version
		t1 := time.Now()
		err := config.MigrateConfig(ctx)
		if err != nil {
			return nil, err
		}
		migrationT = time.Since(t1)
	}

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

	serverOpts := []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(
			grpc_prometheus.StreamServerInterceptor,
			streamNamespaceInterceptor,
		)),
		grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
			grpc_prometheus.UnaryServerInterceptor,
			unaryNamespaceInterceptor,
		)),
	}
	if config.GRPC.MaxRecvMsgSize > 0 {
		serverOpts = append(serverOpts, grpc.MaxRecvMsgSize(config.GRPC.MaxRecvMsgSize))
	}
	if config.GRPC.MaxSendMsgSize > 0 {
		serverOpts = append(serverOpts, grpc.MaxSendMsgSize(config.GRPC.MaxSendMsgSize))
	}
	ttrpcServer, err := newTTRPCServer()
	if err != nil {
		return nil, err
	}
	tcpServerOpts := serverOpts
	if config.GRPC.TCPTLSCert != "" {
		log.G(ctx).Info("setting up tls on tcp GRPC services...")

		tlsCert, err := tls.LoadX509KeyPair(config.GRPC.TCPTLSCert, config.GRPC.TCPTLSKey)
		if err != nil {
			return nil, err
		}
		tlsConfig := &tls.Config{Certificates: []tls.Certificate{tlsCert}}

		if config.GRPC.TCPTLSCA != "" {
			caCertPool := x509.NewCertPool()
			caCert, err := os.ReadFile(config.GRPC.TCPTLSCA)
			if err != nil {
				return nil, fmt.Errorf("failed to load CA file: %w", err)
			}
			caCertPool.AppendCertsFromPEM(caCert)
			tlsConfig.ClientCAs = caCertPool
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		}

		tcpServerOpts = append(tcpServerOpts, grpc.Creds(credentials.NewTLS(tlsConfig)))
	}

	// grpcService allows GRPC services to be registered with the underlying server
	type grpcService interface {
		Register(*grpc.Server) error
	}

	// tcpService allows GRPC services to be registered with the underlying tcp server
	type tcpService interface {
		RegisterTCP(*grpc.Server) error
	}

	// ttrpcService allows TTRPC services to be registered with the underlying server
	type ttrpcService interface {
		RegisterTTRPC(*ttrpc.Server) error
	}

	var (
		grpcServer = grpc.NewServer(serverOpts...)
		tcpServer  = grpc.NewServer(tcpServerOpts...)

		grpcServices  []grpcService
		tcpServices   []tcpService
		ttrpcServices []ttrpcService

		s = &Server{
			grpcServer:  grpcServer,
			tcpServer:   tcpServer,
			ttrpcServer: ttrpcServer,
			config:      config,
		}
		initialized = plugin.NewPluginSet()
		required    = make(map[string]struct{})
	)
	for _, r := range config.RequiredPlugins {
		required[r] = struct{}{}
	}

	if version < srvconfig.CurrentConfigVersion {
		t1 := time.Now()
		// Run migration for each configuration version
		// Run each plugin migration for each version to ensure that migration logic is simple and
		// focused on upgrading from one version at a time.
		for v := version; v < srvconfig.CurrentConfigVersion; v++ {
			for _, p := range loaded {
				if p.ConfigMigration != nil {
					if err := p.ConfigMigration(ctx, v, config.Plugins); err != nil {
						return nil, err
					}
				}
			}
		}
		migrationT = migrationT + time.Since(t1)
	}
	if migrationT > 0 {
		log.G(ctx).WithField("t", migrationT).Warnf("Configuration migrated from version %d, use `containerd config migrate` to avoid migration", version)
	}

	for _, p := range loaded {
		id := p.URI()
		log.G(ctx).WithFields(log.Fields{"id": id, "type": p.Type}).Info("loading plugin")
		var mustSucceed int32

		initContext := plugin.NewContext(
			ctx,
			initialized,
			map[string]string{
				plugins.PropertyRootDir:      filepath.Join(config.Root, id),
				plugins.PropertyStateDir:     filepath.Join(config.State, id),
				plugins.PropertyGRPCAddress:  config.GRPC.Address,
				plugins.PropertyTTRPCAddress: config.TTRPC.Address,
			},
		)
		initContext.RegisterReadiness = func() func() {
			atomic.StoreInt32(&mustSucceed, 1)
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
			if atomic.LoadInt32(&mustSucceed) != 0 {
				return nil, fmt.Errorf("plugin failed after registering readiness %s: %w", id, err)
			}
			continue
		}

		delete(required, id)
		// check for grpc services that should be registered with the server
		if src, ok := instance.(grpcService); ok {
			grpcServices = append(grpcServices, src)
		}
		if src, ok := instance.(ttrpcService); ok {
			ttrpcServices = append(ttrpcServices, src)
		}
		if service, ok := instance.(tcpService); ok {
			tcpServices = append(tcpServices, service)
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

	// register services after all plugins have been initialized
	for _, service := range grpcServices {
		if err := service.Register(grpcServer); err != nil {
			return nil, err
		}
	}
	for _, service := range ttrpcServices {
		if err := service.RegisterTTRPC(ttrpcServer); err != nil {
			return nil, err
		}
	}
	for _, service := range tcpServices {
		if err := service.RegisterTCP(tcpServer); err != nil {
			return nil, err
		}
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

	if config.PluginDir != "" { //nolint:staticcheck
		warn.Emit(ctx, deprecation.GoPluginLibrary)
	}
}

// Server is the containerd main daemon
type Server struct {
	grpcServer  *grpc.Server
	ttrpcServer *ttrpc.Server
	tcpServer   *grpc.Server
	config      *srvconfig.Config
	plugins     []*plugin.Plugin
	ready       sync.WaitGroup
}

// ServeGRPC provides the containerd grpc APIs on the provided listener
func (s *Server) ServeGRPC(l net.Listener) error {
	if s.config.Metrics.GRPCHistogram {
		// enable grpc time histograms to measure rpc latencies
		grpc_prometheus.EnableHandlingTimeHistogram()
	}
	// before we start serving the grpc API register the grpc_prometheus metrics
	// handler.  This needs to be the last service registered so that it can collect
	// metrics for every other service
	grpc_prometheus.Register(s.grpcServer)
	return trapClosedConnErr(s.grpcServer.Serve(l))
}

// ServeTTRPC provides the containerd ttrpc APIs on the provided listener
func (s *Server) ServeTTRPC(l net.Listener) error {
	return trapClosedConnErr(s.ttrpcServer.Serve(context.Background(), l))
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

// ServeTCP allows services to serve over tcp
func (s *Server) ServeTCP(l net.Listener) error {
	grpc_prometheus.Register(s.tcpServer)
	return trapClosedConnErr(s.tcpServer.Serve(l))
}

// ServeDebug provides a debug endpoint
func (s *Server) ServeDebug(l net.Listener) error {
	// don't use the default http server mux to make sure nothing gets registered
	// that we don't want to expose via containerd
	m := http.NewServeMux()
	m.Handle("/debug/vars", expvar.Handler())
	m.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
	m.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
	m.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
	m.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
	m.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	srv := &http.Server{
		Handler:           m,
		ReadHeaderTimeout: 5 * time.Minute, // "G112: Potential Slowloris Attack (gosec)"; not a real concern for our use, so setting a long timeout.
	}
	return trapClosedConnErr(srv.Serve(l))
}

// Stop the containerd server canceling any open connections
func (s *Server) Stop() {
	s.grpcServer.Stop()
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
	path := config.PluginDir //nolint:staticcheck
	if path == "" {
		path = filepath.Join(config.Root, "plugins")
	}
	if count, err := dynamic.Load(path); err != nil {
		return nil, err
	} else if count > 0 || config.PluginDir != "" { //nolint:staticcheck
		config.PluginDir = path //nolint:staticcheck
		log.G(ctx).Warningf("loaded %d dynamic plugins. `go_plugin` is deprecated, please use `external plugins` instead", count)
	}
	// load additional plugins that don't automatically register themselves
	registry.Register(&plugin.Registration{
		Type: plugins.ContentPlugin,
		ID:   "content",
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			root := ic.Properties[plugins.PropertyRootDir]
			ic.Meta.Exports["root"] = root
			return local.NewStore(root)
		},
	})

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
				return csproxy.NewContentStore(csapi.NewContentClient(conn))
			}
		case string(plugins.SandboxControllerPlugin), "sandbox":
			t = plugins.SandboxControllerPlugin
			f = func(conn *grpc.ClientConn) interface{} {
				return sbproxy.NewSandboxController(sbapi.NewControllerClient(conn))
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

	conn, err := grpc.Dial(dialer.DialAddress(address), gopts...)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %q: %w", address, err)
	}

	pc.clients[address] = conn

	return conn, nil
}

func trapClosedConnErr(err error) error {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
