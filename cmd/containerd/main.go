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

var global = log.WithModule(gocontext.Background(), "containerd")

func main() {
	app := cli.NewApp()
	app.Name = "containerd"
	app.Version = containerd.Version
	app.Usage = usage
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "log-level",
			Usage: "Set the logging level [debug, info, warn, error, fatal, panic]",
			Value: "info",
		},
		cli.StringFlag{
			Name:  "root",
			Usage: "containerd state directory",
			Value: "/run/containerd",
		},
		cli.StringFlag{
			Name:  "socket, s",
			Usage: "socket path for containerd's GRPC server",
			Value: "/run/containerd/containerd.sock",
		},
		cli.StringFlag{
			Name:  "debug-socket, d",
			Usage: "socket path for containerd's debug server",
			Value: "/run/containerd/containerd-debug.sock",
		},
		cli.StringFlag{
			Name:  "metrics-address, m",
			Usage: "tcp address to serve metrics on",
			Value: "127.0.0.1:7897",
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
		runtimes, err := loadRuntimes(context)
		if err != nil {
			return err
		}
		supervisor, err := containerd.NewSupervisor(log.WithModule(global, "execution"), runtimes)
		if err != nil {
			return err
		}
		// start debug and metrics APIs
		if err := serveDebugAPI(context); err != nil {
			return err
		}
		serveMetricsAPI(context)
		// start the GRPC api with the execution service registered
		server := newGRPCServer(execution.New(supervisor))
		if err := serveGRPC(context, server); err != nil {
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
	if l := context.GlobalString("log-level"); l != "" {
		lvl, err := logrus.ParseLevel(l)
		if err != nil {
			lvl = logrus.InfoLevel
			fmt.Fprintf(os.Stderr, "Unable to parse logging level: %s\n, and being defaulted to info", l)
		}
		logrus.SetLevel(lvl)
	}
	return nil
}

func serveMetricsAPI(context *cli.Context) {
	if addr := context.GlobalString("metrics-address"); addr != "" {
		log.G(global).WithField("metrics", addr).Info("starting metrics API...")
		h := newMetricsHandler()
		go func() {
			if err := http.ListenAndServe(addr, h); err != nil {
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

func serveDebugAPI(context *cli.Context) error {
	path := context.GlobalString("debug-socket")
	if path == "" {
		return errors.New("--debug-socket path cannot be empty")
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

func loadRuntimes(context *cli.Context) (map[string]containerd.Runtime, error) {
	var (
		root = context.GlobalString("root")
		o    = make(map[string]containerd.Runtime)
	)
	for _, name := range containerd.Runtimes() {
		r, err := containerd.NewRuntime(name, root)
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

func serveGRPC(context *cli.Context, server *grpc.Server) error {
	path := context.GlobalString("socket")
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
