package main

import (
	_ "expvar"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	gocontext "golang.org/x/net/context"
	"google.golang.org/grpc"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd"
	contentapi "github.com/docker/containerd/api/services/content"
	api "github.com/docker/containerd/api/services/execution"
	"github.com/docker/containerd/content"
	"github.com/docker/containerd/log"
	"github.com/docker/containerd/plugin"
	"github.com/docker/containerd/reaper"
	"github.com/docker/containerd/snapshot"
	"github.com/docker/containerd/sys"
	"github.com/docker/containerd/utils"
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
		fmt.Println(c.App.Name, containerd.Package, c.App.Version)
	}
}

func main() {
	app := cli.NewApp()
	app.Name = "containerd"
	app.Version = containerd.Version
	app.Usage = usage
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "config,c",
			Usage: "path to the configuration file",
			Value: "/etc/containerd/config.toml",
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
			Name:  "socket,s",
			Usage: "socket path for containerd's GRPC server",
		},
		cli.StringFlag{
			Name:  "root",
			Usage: "containerd root directory",
		},
	}
	app.Before = before
	app.Action = func(context *cli.Context) error {
		start := time.Now()
		// start the signal handler as soon as we can to make sure that
		// we don't miss any signals during boot
		signals := make(chan os.Signal, 2048)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1, syscall.SIGCHLD)
		if conf.Subreaper {
			log.G(global).Info("setting subreaper...")
			if err := sys.SetSubreaper(1); err != nil {
				return err
			}
		}
		log.G(global).Info("starting containerd boot...")

		// load all plugins into containerd
		if err := plugin.Load(filepath.Join(conf.Root, "plugins")); err != nil {
			return err
		}
		// start debug and metrics APIs
		if err := serveDebugAPI(); err != nil {
			return err
		}
		runtimes, err := loadRuntimes()
		if err != nil {
			return err
		}
		store, err := resolveContentStore()
		if err != nil {
			return err
		}
		snapshotter, err := loadSnapshotter(store)
		if err != nil {
			return err
		}
		services, err := loadServices(runtimes, store, snapshotter)
		if err != nil {
			return err
		}
		// start the GRPC api with the execution service registered
		server := newGRPCServer()
		for _, service := range services {
			if err := service.Register(server); err != nil {
				return err
			}
		}
		log.G(global).Info("starting GRPC API server...")
		if err := serveGRPC(server); err != nil {
			return err
		}
		// start the prometheus metrics API for containerd
		serveMetricsAPI()

		log.G(global).Infof("containerd successfully booted in %fs", time.Now().Sub(start).Seconds())
		return handleSignals(signals, server)
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "containerd: %s\n", err)
		os.Exit(1)
	}
}

func before(context *cli.Context) error {
	if err := loadConfig(context.GlobalString("config")); err != nil &&
		!os.IsNotExist(err) {
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
			name: "state",
			d:    &conf.State,
		},
		{
			name: "socket",
			d:    &conf.GRPC.Socket,
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
	path := conf.Debug.Socket
	if path == "" {
		return errors.New("debug socket path cannot be empty")
	}
	l, err := utils.CreateUnixSocket(path)
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

func resolveContentStore() (*content.Store, error) {
	cp := filepath.Join(conf.Root, "content")
	return content.NewStore(cp)
}

func loadRuntimes() (map[string]containerd.Runtime, error) {
	o := make(map[string]containerd.Runtime)
	for name, rr := range plugin.Registrations() {
		if rr.Type != plugin.RuntimePlugin {
			continue
		}
		log.G(global).Infof("loading runtime plugin %q...", name)
		ic := &plugin.InitContext{
			Root:    conf.Root,
			State:   conf.State,
			Context: log.WithModule(global, fmt.Sprintf("runtime-%s", name)),
		}
		if rr.Config != nil {
			if err := conf.decodePlugin(name, rr.Config); err != nil {
				return nil, err
			}
			ic.Config = rr.Config
		}
		vr, err := rr.Init(ic)
		if err != nil {
			return nil, err
		}
		o[name] = vr.(containerd.Runtime)
	}
	return o, nil
}

func loadSnapshotter(store *content.Store) (snapshot.Snapshotter, error) {
	for name, sr := range plugin.Registrations() {
		if sr.Type != plugin.SnapshotPlugin {
			continue
		}
		moduleName := fmt.Sprintf("snapshot-%s", conf.Snapshotter)
		if name != moduleName {
			continue
		}

		log.G(global).Infof("loading snapshot plugin %q...", name)
		ic := &plugin.InitContext{
			Root:    conf.Root,
			State:   conf.State,
			Store:   store,
			Context: log.WithModule(global, moduleName),
		}
		if sr.Config != nil {
			if err := conf.decodePlugin(name, sr.Config); err != nil {
				return nil, err
			}
			ic.Config = sr.Config
		}
		sn, err := sr.Init(ic)
		if err != nil {
			return nil, err
		}

		return sn.(snapshot.Snapshotter), nil
	}
	return nil, fmt.Errorf("snapshotter not loaded: %v", conf.Snapshotter)
}

func newGRPCServer() *grpc.Server {
	s := grpc.NewServer(grpc.UnaryInterceptor(interceptor))
	return s
}

func loadServices(runtimes map[string]containerd.Runtime, store *content.Store, sn snapshot.Snapshotter) ([]plugin.Service, error) {
	var o []plugin.Service
	for name, sr := range plugin.Registrations() {
		if sr.Type != plugin.GRPCPlugin {
			continue
		}
		log.G(global).Infof("loading grpc service plugin %q...", name)
		ic := &plugin.InitContext{
			Root:        conf.Root,
			State:       conf.State,
			Context:     log.WithModule(global, fmt.Sprintf("service-%s", name)),
			Runtimes:    runtimes,
			Store:       store,
			Snapshotter: sn,
		}
		if sr.Config != nil {
			if err := conf.decodePlugin(name, sr.Config); err != nil {
				return nil, err
			}
			ic.Config = sr.Config
		}
		vs, err := sr.Init(ic)
		if err != nil {
			return nil, err
		}
		o = append(o, vs.(plugin.Service))
	}
	return o, nil
}

func serveGRPC(server *grpc.Server) error {
	path := conf.GRPC.Socket
	if path == "" {
		return errors.New("--socket path cannot be empty")
	}
	l, err := utils.CreateUnixSocket(path)
	if err != nil {
		return err
	}
	if err := os.Chown(path, conf.GRPC.Uid, conf.GRPC.Gid); err != nil {
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
	case api.ContainerServiceServer:
		ctx = log.WithModule(ctx, "execution")
	case contentapi.ContentServer:
		ctx = log.WithModule(ctx, "content")
	default:
		fmt.Printf("unknown GRPC server type: %#v\n", info.Server)
	}
	return handler(ctx, req)
}

func handleSignals(signals chan os.Signal, server *grpc.Server) error {
	for s := range signals {
		log.G(global).WithField("signal", s).Debug("received signal")
		switch s {
		case syscall.SIGCHLD:
			if _, err := reaper.Reap(); err != nil {
				log.G(global).WithError(err).Error("reap containerd processes")
			}
		default:
			server.Stop()
			return nil
		}
	}
	return nil
}
