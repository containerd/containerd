package main

import (
	_ "expvar"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	gocontext "golang.org/x/net/context"
	"google.golang.org/grpc"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd"
	api "github.com/docker/containerd/api/services/execution"
	"github.com/docker/containerd/events"
	"github.com/docker/containerd/log"
	"github.com/docker/containerd/supervisor"
	"github.com/docker/containerd/utils"
	metrics "github.com/docker/go-metrics"
	"github.com/pkg/errors"
	"github.com/urfave/cli"

	natsd "github.com/nats-io/gnatsd/server"
	"github.com/nats-io/go-nats"
	stand "github.com/nats-io/nats-streaming-server/server"
)

const usage = `
                    __        _                     __
  _________  ____  / /_____ _(_)___  ___  _________/ /
 / ___/ __ \/ __ \/ __/ __ ` + "`" + `/ / __ \/ _ \/ ___/ __  /
/ /__/ /_/ / / / / /_/ /_/ / / / / /  __/ /  / /_/ /
\___/\____/_/ /_/\__/\__,_/_/_/ /_/\___/_/   \__,_/

high performance container runtime
`

const (
	StanClusterID = "containerd"
	stanClientID  = "containerd"
)

func main() {
	app := cli.NewApp()
	app.Name = "containerd"
	app.Version = containerd.Version
	app.Usage = usage
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "enable debug output in logs",
		},
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
		cli.StringFlag{
			Name:  "events-address, e",
			Usage: "nats address to serve events on",
			Value: nats.DefaultURL,
		},
	}
	app.Before = func(context *cli.Context) error {
		if context.GlobalBool("debug") {
			logrus.SetLevel(logrus.DebugLevel)
		}
		if logLevel := context.GlobalString("log-level"); logLevel != "" {
			lvl, err := logrus.ParseLevel(logLevel)
			if err != nil {
				lvl = logrus.InfoLevel
				fmt.Fprintf(os.Stderr, "Unable to parse logging level: %s\n, and being defaulted to info", logLevel)
			}
			logrus.SetLevel(lvl)
		}
		return nil
	}
	app.Action = func(context *cli.Context) error {
		signals := make(chan os.Signal, 2048)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1)

		ctx := log.WithModule(gocontext.Background(), "containerd")
		if address := context.GlobalString("metrics-address"); address != "" {
			log.G(ctx).WithField("metrics-address", address).Info("listening and serving metrics")
			go serveMetrics(ctx, address)
		}

		ea := context.GlobalString("events-address")
		log.G(ctx).WithField("events-address", ea).Info("starting nats-streaming-server")
		s, err := startNATSServer(ea)
		if err != nil {
			return nil
		}
		defer s.Shutdown()

		debugPath := context.GlobalString("debug-socket")
		if debugPath == "" {
			return errors.New("--debug-socket path cannot be empty")
		}
		d, err := utils.CreateUnixSocket(debugPath)
		if err != nil {
			return err
		}

		//publish profiling and debug socket.
		log.G(ctx).WithField("socket", debugPath).Info("starting profiler handlers")
		log.G(ctx).WithFields(logrus.Fields{"expvars": "/debug/vars", "socket": debugPath}).Debug("serving expvars requests")
		log.G(ctx).WithFields(logrus.Fields{"pprof": "/debug/pprof", "socket": debugPath}).Debug("serving pprof requests")
		go serveProfiler(ctx, d)

		path := context.GlobalString("socket")
		if path == "" {
			return errors.New("--socket path cannot be empty")
		}
		l, err := utils.CreateUnixSocket(path)
		if err != nil {
			return err
		}

		// Get events publisher
		natsPoster, err := events.NewNATSPoster(StanClusterID, stanClientID)
		if err != nil {
			return err
		}
		execCtx := log.WithModule(ctx, "execution")
		execCtx = events.WithPoster(execCtx, natsPoster)
		execService, err := supervisor.New(execCtx, context.GlobalString("root"))
		if err != nil {
			return err
		}

		// Intercept the GRPC call in order to populate the correct module path
		interceptor := func(ctx gocontext.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
			ctx = log.WithModule(ctx, "containerd")
			switch info.Server.(type) {
			case api.ExecutionServiceServer:
				ctx = log.WithModule(ctx, "execution")
				ctx = events.WithPoster(ctx, natsPoster)
			default:
				fmt.Printf("Unknown type: %#v\n", info.Server)
			}
			return handler(ctx, req)
		}
		server := grpc.NewServer(grpc.UnaryInterceptor(interceptor))
		api.RegisterExecutionServiceServer(server, execService)
		log.G(ctx).WithField("socket", l.Addr()).Info("start serving GRPC API")
		go serveGRPC(ctx, server, l)

		for s := range signals {
			switch s {
			case syscall.SIGUSR1:
				dumpStacks(ctx)
			default:
				log.G(ctx).WithField("signal", s).Info("stopping GRPC server")
				server.Stop()
				return nil
			}
		}
		return nil
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "containerd: %s\n", err)
		os.Exit(1)
	}
}

func serveMetrics(ctx gocontext.Context, address string) {
	m := http.NewServeMux()
	m.Handle("/metrics", metrics.Handler())
	if err := http.ListenAndServe(address, m); err != nil {
		log.G(ctx).WithError(err).Fatal("metrics server failure")
	}
}

func serveGRPC(ctx gocontext.Context, server *grpc.Server, l net.Listener) {
	defer l.Close()
	if err := server.Serve(l); err != nil {
		log.G(ctx).WithError(err).Fatal("GRPC server failure")
	}
}

func serveProfiler(ctx gocontext.Context, l net.Listener) {
	defer l.Close()
	if err := http.Serve(l, nil); err != nil {
		log.G(ctx).WithError(err).Fatal("profiler server failure")
	}
}

// DumpStacks dumps the runtime stack.
func dumpStacks(ctx gocontext.Context) {
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
	log.G(ctx).Infof("=== BEGIN goroutine stack dump ===\n%s\n=== END goroutine stack dump ===", buf)
}

func startNATSServer(address string) (s *stand.StanServer, err error) {
	defer func() {
		if r := recover(); r != nil {
			s = nil
			if _, ok := r.(error); !ok {
				err = fmt.Errorf("failed to start NATS server: %v", r)
			} else {
				err = r.(error)
			}
		}
	}()
	so, no, err := getServerOptions(address)
	if err != nil {
		return nil, err
	}
	s = stand.RunServerWithOpts(so, no)

	return s, err
}

func getServerOptions(address string) (*stand.Options, *natsd.Options, error) {
	url, err := url.Parse(address)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed to parse address url %q", address)
	}

	no := stand.DefaultNatsServerOptions
	parts := strings.Split(url.Host, ":")
	if len(parts) == 2 {
		no.Port, err = strconv.Atoi(parts[1])
	} else {
		no.Port = nats.DefaultPort
	}
	no.Host = parts[0]

	so := stand.GetDefaultOptions()
	so.ID = StanClusterID

	return so, &no, nil
}
