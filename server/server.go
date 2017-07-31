package server

import (
	"errors"
	"expvar"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"

	"github.com/boltdb/bolt"
	containers "github.com/containerd/containerd/api/services/containers/v1"
	content "github.com/containerd/containerd/api/services/content/v1"
	diff "github.com/containerd/containerd/api/services/diff/v1"
	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	images "github.com/containerd/containerd/api/services/images/v1"
	namespaces "github.com/containerd/containerd/api/services/namespaces/v1"
	snapshot "github.com/containerd/containerd/api/services/snapshot/v1"
	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	version "github.com/containerd/containerd/api/services/version/v1"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	metrics "github.com/docker/go-metrics"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"golang.org/x/net/context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// New creates and initializes a new containerd server
func New(ctx context.Context, config *Config) (*Server, error) {
	if config.Root == "" {
		return nil, errors.New("root must be specified")
	}
	if err := os.MkdirAll(config.Root, 0711); err != nil {
		return nil, err
	}
	if err := apply(ctx, config); err != nil {
		return nil, err
	}
	plugins, err := loadPlugins(ctx, config)
	if err != nil {
		return nil, err
	}
	rpc := grpc.NewServer(
		grpc.UnaryInterceptor(interceptor),
		grpc.StreamInterceptor(grpc_prometheus.StreamServerInterceptor),
	)
	var (
		services []plugin.Service
		s        = &Server{
			rpc:    rpc,
			events: events.NewExchange(),
		}
		initialized = make(map[plugin.PluginType]map[string]interface{})
	)
	for _, p := range plugins {
		id := p.URI()
		log.G(ctx).WithField("type", p.Type).Infof("loading plugin %q...", id)

		initContext := plugin.NewContext(
			ctx,
			initialized,
			config.Root,
			id,
		)
		initContext.Events = s.events
		initContext.Address = config.GRPC.Address

		// load the plugin specific configuration if it is provided
		if p.Config != nil {
			pluginConfig, err := config.Decode(p.ID, p.Config)
			if err != nil {
				return nil, err
			}
			initContext.Config = pluginConfig
		}
		instance, err := p.Init(initContext)
		if err != nil {
			if plugin.IsSkipPlugin(err) {
				log.G(ctx).WithField("type", p.Type).Infof("skip loading plugin %q...", id)
			} else {
				log.G(ctx).WithError(err).Warnf("failed to load plugin %s", id)
			}
			continue
		}

		if types, ok := initialized[p.Type]; ok {
			types[p.ID] = instance
		} else {
			initialized[p.Type] = map[string]interface{}{
				p.ID: instance,
			}
		}
		// check for grpc services that should be registered with the server
		if service, ok := instance.(plugin.Service); ok {
			services = append(services, service)
		}
	}
	// register services after all plugins have been initialized
	for _, service := range services {
		if err := service.Register(rpc); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Server is the containerd main daemon
type Server struct {
	rpc    *grpc.Server
	events *events.Exchange
}

// ServeGRPC provides the containerd grpc APIs on the provided listener
func (s *Server) ServeGRPC(l net.Listener) error {
	// before we start serving the grpc API regster the grpc_prometheus metrics
	// handler.  This needs to be the last service registered so that it can collect
	// metrics for every other service
	grpc_prometheus.Register(s.rpc)
	return s.rpc.Serve(l)
}

// ServeMetrics provides a prometheus endpoint for exposing metrics
func (s *Server) ServeMetrics(l net.Listener) error {
	m := http.NewServeMux()
	m.Handle("/metrics", metrics.Handler())
	return http.Serve(l, m)
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
	return http.Serve(l, m)
}

// Stop gracefully stops the containerd server
func (s *Server) Stop() {
	s.rpc.GracefulStop()
}

func loadPlugins(ctx context.Context, config *Config) ([]*plugin.Registration, error) {
	onPluginOpenError := func(dllPath string, openError error) error {
		// typically, openError is "plugin.Open: plugin was built with a different version of package..."
		log.G(ctx).WithError(openError).Warnf("could not load %s", dllPath)
		// ignore and continue
		return nil
	}
	// load all plugins into containerd
	if err := plugin.Load(filepath.Join(config.Root, "plugins"), onPluginOpenError); err != nil {
		return nil, err
	}
	// load additional plugins that don't automatically register themselves
	plugin.Register(&plugin.Registration{
		Type: plugin.ContentPlugin,
		ID:   "content",
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			return local.NewStore(ic.Root)
		},
	})
	plugin.Register(&plugin.Registration{
		Type: plugin.MetadataPlugin,
		ID:   "bolt",
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			if err := os.MkdirAll(ic.Root, 0711); err != nil {
				return nil, err
			}
			return bolt.Open(filepath.Join(ic.Root, "meta.db"), 0644, nil)
		},
	})

	// return the ordered graph for plugins
	return plugin.Graph(), nil
}

func interceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	ctx = log.WithModule(ctx, "containerd")
	switch info.Server.(type) {
	case tasks.TasksServer:
		ctx = log.WithModule(ctx, "tasks")
	case containers.ContainersServer:
		ctx = log.WithModule(ctx, "containers")
	case content.ContentServer:
		ctx = log.WithModule(ctx, "content")
	case images.ImagesServer:
		ctx = log.WithModule(ctx, "images")
	case grpc_health_v1.HealthServer:
		// No need to change the context
	case version.VersionServer:
		ctx = log.WithModule(ctx, "version")
	case snapshot.SnapshotsServer:
		ctx = log.WithModule(ctx, "snapshot")
	case diff.DiffServer:
		ctx = log.WithModule(ctx, "diff")
	case namespaces.NamespacesServer:
		ctx = log.WithModule(ctx, "namespaces")
	case eventsapi.EventsServer:
		ctx = log.WithModule(ctx, "events")
	default:
		log.G(ctx).Warnf("unknown GRPC server type: %#v\n", info.Server)
	}
	return grpc_prometheus.UnaryServerInterceptor(ctx, req, info, handler)
}
