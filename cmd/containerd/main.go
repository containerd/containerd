package main

import (
	_ "expvar"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	gocontext "golang.org/x/net/context"
	"google.golang.org/grpc"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd"
	api "github.com/docker/containerd/api/services/execution"
	_ "github.com/docker/containerd/linux"
	"github.com/docker/containerd/log"
	"github.com/docker/containerd/services/execution"
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
			Name:  "root,r",
			Usage: "containerd root directory",
		},
		cli.StringFlag{
			Name:  "state",
			Usage: "containerd state directory",
		},
		cli.StringFlag{
			Name:  "socket,s",
			Usage: "socket path for containerd's GRPC server",
		},
	}
	app.Before = before
	app.Action = func(context *cli.Context) error {
		start := time.Now()
		// start the signal handler as soon as we can to make sure that
		// we don't miss any signals during boot
		signals := make(chan os.Signal, 2048)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1)

		log.G(global).Info("starting containerd boot...")
		runtimes, err := loadRuntimes()
		if err != nil {
			return err
		}
		supervisor, err := containerd.NewSupervisor(log.WithModule(global, "execution"), runtimes)
		if err != nil {
			return err
		}
		// start debug and metrics APIs
		if err := serveDebugAPI(); err != nil {
			return err
		}
		serveMetricsAPI()
		// start the GRPC api with the execution service registered
		server := newGRPCServer(execution.New(supervisor))
		if err := serveGRPC(server); err != nil {
			return err
		}
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

func loadRuntimes() (map[string]containerd.Runtime, error) {
	o := make(map[string]containerd.Runtime)
	for _, name := range containerd.Runtimes() {
		r, err := containerd.NewRuntime(name, conf.State)
		if err != nil {
			return nil, err
		}
		o[name] = r
		log.G(global).WithField("runtime", name).Info("load runtime")
	}
	return o, nil
}

func newGRPCServer(service api.ContainerServiceServer) *grpc.Server {
	s := grpc.NewServer(grpc.UnaryInterceptor(interceptor))
	api.RegisterContainerServiceServer(s, service)
	return s
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
		ctx = log.WithModule(global, "execution")
	default:
		fmt.Printf("unknown GRPC server type: %#v\n", info.Server)
	}
	return handler(ctx, req)
}

func handleSignals(signals chan os.Signal, server *grpc.Server) error {
	for s := range signals {
		log.G(global).WithField("signal", s).Debug("received signal")
		switch s {
		default:
			server.Stop()
			return nil
		}
	}
	return nil
}
