package main

import (
	_ "expvar"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"github.com/boltdb/bolt"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	gocontext "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/Sirupsen/logrus"
	containersapi "github.com/containerd/containerd/api/services/containers"
	contentapi "github.com/containerd/containerd/api/services/content"
	diffapi "github.com/containerd/containerd/api/services/diff"
	api "github.com/containerd/containerd/api/services/execution"
	imagesapi "github.com/containerd/containerd/api/services/images"
	namespacesapi "github.com/containerd/containerd/api/services/namespaces"
	snapshotapi "github.com/containerd/containerd/api/services/snapshot"
	versionapi "github.com/containerd/containerd/api/services/version"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/sys"
	"github.com/containerd/containerd/version"
	metrics "github.com/docker/go-metrics"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

const usage = `
                    __        _                     __
  _________  ____  / /_____ _(_)___  ___  _________/ /
 / ___/ __ \/ __ \/ __/ __ ` + "`" + `/ / __ \/ _ \/ ___/ __  /
/ /__/ /_/ / / / / /_/ /_/ / / / / /  __/ /  / /_/ /
\___/\____/_/ /_/\__/\__,_/_/_/ /_/\___/_/   \__,_/

high performance container runtime
`

var (
	conf   = defaultConfig()
	global = log.WithModule(gocontext.Background(), "containerd")
)

func init() {
	cli.VersionPrinter = func(c *cli.Context) {
		fmt.Println(c.App.Name, version.Package, c.App.Version)
	}
}

func main() {
	app := cli.NewApp()
	app.Name = "containerd"
	app.Version = version.Version
	app.Usage = usage
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "config,c",
			Usage: "path to the configuration file",
			Value: defaultConfigPath,
		},
		cli.StringFlag{
			Name:  "log-level,l",
			Usage: "set the logging level [debug, info, warn, error, fatal, panic]",
		},
		cli.StringFlag{
			Name:  "state",
			Usage: "containerd state directory",
		},
		cli.StringFlag{
			Name:  "address,a",
			Usage: "address for containerd's GRPC server",
		},
		cli.StringFlag{
			Name:  "root",
			Usage: "containerd root directory",
		},
	}
	app.Commands = []cli.Command{
		configCommand,
	}
	app.Before = before
	app.Action = func(context *cli.Context) error {
		start := time.Now()
		// start the signal handler as soon as we can to make sure that
		// we don't miss any signals during boot
		signals := make(chan os.Signal, 2048)
		signal.Notify(signals, handledSignals...)
		if err := platformInit(context); err != nil {
			return err
		}
		log.G(global).Info("starting containerd boot...")

		// load all plugins into containerd
		if err := plugin.Load(filepath.Join(conf.Root, "plugins")); err != nil {
			return err
		}
		registerContentStore()
		registerMetaDB()
		// start debug and metrics APIs
		if err := serveDebugAPI(); err != nil {
			return err
		}

		var (
			services []plugin.Service
			plugins  = make(map[plugin.PluginType][]interface{})
		)
		for _, init := range plugin.Graph() {
			id := init.URI()
			if !shouldLoad(init) {
				log.G(global).WithField("type", init.Type).Infof("skipping plugin %q...", id)
				continue
			}
			log.G(global).WithField("type", init.Type).Infof("loading plugin %q...", id)
			ic := plugin.NewContext(plugins)
			ic.Root = filepath.Join(conf.Root, id)
			ic.Context = log.WithModule(global, id)
			if init.Config != nil {
				if err := loadPluginConfig(init.ID, init.Config, ic); err != nil {
					return err
				}
			}

			p, err := init.Init(ic)
			if err != nil {
				return err
			}
			plugins[init.Type] = append(plugins[init.Type], p)
			if s, ok := p.(plugin.Service); ok {
				services = append(services, s)
			}
		}

		// start the GRPC api with the execution service registered
		server := newGRPCServer()
		for _, service := range services {
			if err := service.Register(server); err != nil {
				return err
			}
		}
		// register metrics last after all other services
		grpc_prometheus.Register(server)
		log.G(global).Info("starting GRPC API server...")
		if err := serveGRPC(server); err != nil {
			return err
		}
		// start the prometheus metrics API for containerd
		serveMetricsAPI()

		log.G(global).Infof("containerd successfully booted in %fs", time.Since(start).Seconds())
		return handleSignals(signals, server)
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "containerd: %s\n", err)
		os.Exit(1)
	}
}

func before(context *cli.Context) error {
	err := loadConfig(context.GlobalString("config"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// the order for config vs flag values is that flags will always override
	// the config values if they are set
	if err := setLevel(context); err != nil {
		return err
	}
	for _, v := range []struct {
		name string
		d    *string
	}{
		{
			name: "root",
			d:    &conf.Root,
		},
		{
			name: "address",
			d:    &conf.GRPC.Address,
		},
	} {
		if s := context.GlobalString(v.name); s != "" {
			*v.d = s
		}
	}
	return nil
}

func setLevel(context *cli.Context) error {
	l := context.GlobalString("log-level")
	if l == "" {
		l = conf.Debug.Level
	}
	if l != "" {
		lvl, err := logrus.ParseLevel(l)
		if err != nil {
			return err
		}
		logrus.SetLevel(lvl)
	}
	return nil
}

func serveMetricsAPI() {
	if conf.Metrics.Address != "" {
		log.G(global).WithField("metrics", conf.Metrics.Address).Info("starting metrics API...")
		h := newMetricsHandler()
		go func() {
			if err := http.ListenAndServe(conf.Metrics.Address, h); err != nil {
				log.G(global).WithError(err).Fatal("serve metrics API")
			}
		}()
	}
}

func newMetricsHandler() http.Handler {
	m := http.NewServeMux()
	m.Handle("/metrics", metrics.Handler())
	return m
}

func serveDebugAPI() error {
	path := conf.Debug.Address
	if path == "" {
		return errors.New("debug socket path cannot be empty")
	}
	l, err := sys.GetLocalListener(path, conf.GRPC.Uid, conf.GRPC.Gid)
	if err != nil {
		return err
	}
	log.G(global).WithField("debug", path).Info("starting debug API...")
	go func() {
		defer l.Close()
		// pprof and expvars are imported and automatically register their endpoints
		// under /debug
		if err := http.Serve(l, nil); err != nil {
			log.G(global).WithError(err).Fatal("serve debug API")
		}
	}()
	return nil
}

func registerContentStore() {
	plugin.Register(&plugin.Registration{
		Type: plugin.ContentPlugin,
		ID:   "content",
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			return content.NewStore(ic.Root)
		},
	})
}

func registerMetaDB() {
	plugin.Register(&plugin.Registration{
		Type: plugin.MetadataPlugin,
		ID:   "bolt",
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			if err := os.MkdirAll(ic.Root, 0700); err != nil {
				return nil, err
			}
			return bolt.Open(filepath.Join(ic.Root, "meta.db"), 0644, nil)
		},
	})
}

func newGRPCServer() *grpc.Server {
	s := grpc.NewServer(
		grpc.UnaryInterceptor(interceptor),
		grpc.StreamInterceptor(grpc_prometheus.StreamServerInterceptor),
	)
	return s
}

func serveGRPC(server *grpc.Server) error {
	path := conf.GRPC.Address
	if path == "" {
		return errors.New("--socket path cannot be empty")
	}
	l, err := sys.GetLocalListener(path, conf.GRPC.Uid, conf.GRPC.Gid)
	if err != nil {
		return err
	}
	go func() {
		defer l.Close()
		if err := server.Serve(l); err != nil {
			log.G(global).WithError(err).Fatal("serve GRPC")
		}
	}()
	return nil
}

func interceptor(ctx gocontext.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	ctx = log.WithModule(ctx, "containerd")
	switch info.Server.(type) {
	case api.TasksServer:
		ctx = log.WithModule(ctx, "execution")
	case containersapi.ContainersServer:
		ctx = log.WithModule(ctx, "containers")
	case contentapi.ContentServer:
		ctx = log.WithModule(ctx, "content")
	case imagesapi.ImagesServer:
		ctx = log.WithModule(ctx, "images")
	case grpc_health_v1.HealthServer:
		// No need to change the context
	case versionapi.VersionServer:
		ctx = log.WithModule(ctx, "version")
	case snapshotapi.SnapshotServer:
		ctx = log.WithModule(ctx, "snapshot")
	case diffapi.DiffServer:
		ctx = log.WithModule(ctx, "diff")
	case namespacesapi.NamespacesServer:
		ctx = log.WithModule(ctx, "namespaces")
	default:
		log.G(ctx).Warnf("unknown GRPC server type: %#v\n", info.Server)
	}
	return grpc_prometheus.UnaryServerInterceptor(ctx, req, info, handler)
}

func dumpStacks() {
	var (
		buf       []byte
		stackSize int
	)
	bufferLen := 16384
	for stackSize == len(buf) {
		buf = make([]byte, bufferLen)
		stackSize = runtime.Stack(buf, true)
		bufferLen *= 2
	}
	buf = buf[:stackSize]
	logrus.Infof("=== BEGIN goroutine stack dump ===\n%s\n=== END goroutine stack dump ===", buf)
}

func loadPluginConfig(name string, c interface{}, ic *plugin.InitContext) error {
	if err := conf.decodePlugin(name, c); err != nil {
		return err
	}
	ic.Config = c
	return nil
}

func shouldLoad(r *plugin.Registration) bool {
	// only load certain plugins based on the config values
	switch r.Type {
	case plugin.SnapshotPlugin:
		return r.URI() == conf.Snapshotter
	case plugin.DiffPlugin:
		return r.URI() == conf.Differ
	default:
		return true
	}
}
