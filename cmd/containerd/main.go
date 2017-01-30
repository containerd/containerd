package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	gocontext "golang.org/x/net/context"
	"google.golang.org/grpc"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd"
	api "github.com/docker/containerd/api/execution"
	"github.com/docker/containerd/events"
	"github.com/docker/containerd/execution"
	"github.com/docker/containerd/execution/executors/shim"
	"github.com/docker/containerd/log"
	"github.com/docker/containerd/utils"
	metrics "github.com/docker/go-metrics"
	"github.com/urfave/cli"

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
			Name:  "root",
			Usage: "containerd state directory",
			Value: "/run/containerd",
		},
		cli.StringFlag{
			Name:  "runtime",
			Usage: "runtime for execution",
			Value: "shim",
		},
		cli.StringFlag{
			Name:  "socket, s",
			Usage: "socket path for containerd's GRPC server",
			Value: "/run/containerd/containerd.sock",
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

		path := context.GlobalString("socket")
		if path == "" {
			return fmt.Errorf("--socket path cannot be empty")
		}
		l, err := utils.CreateUnixSocket(path)
		if err != nil {
			return err
		}

		// Get events publisher
		nec, err := getNATSPublisher(ea)
		if err != nil {
			return err
		}
		defer nec.Close()

		var (
			executor execution.Executor
			runtime  = context.GlobalString("runtime")
		)
		log.G(ctx).WithField("runtime", runtime).Info("run with runtime executor")
		execCtx := log.WithModule(ctx, "execution")
		execCtx = events.WithPoster(execCtx, events.GetNATSPoster(nec))
		switch runtime {
		case "shim":
			root := filepath.Join(context.GlobalString("root"), "shim")
			err = os.MkdirAll(root, 0700)
			if err != nil && !os.IsExist(err) {
				return err
			}
			executor, err = shim.New(log.WithModule(execCtx, "shim"), root, "containerd-shim", "runc", nil)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("oci: runtime %q not implemented", runtime)
		}

		execService, err := execution.New(execCtx, executor)
		if err != nil {
			return err
		}

		// Intercept the GRPC call in order to populate the correct module path
		interceptor := func(ctx gocontext.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
			ctx = log.WithModule(ctx, "containerd")
			switch info.Server.(type) {
			case api.ExecutionServiceServer:
				ctx = log.WithModule(ctx, "execution")
				ctx = events.WithPoster(ctx, events.GetNATSPoster(nec))
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

func startNATSServer(eventsAddress string) (e *stand.StanServer, err error) {
	eventsURL, err := url.Parse(eventsAddress)
	if err != nil {
		return nil, err
	}

	no := stand.DefaultNatsServerOptions
	nOpts := &no
	nOpts.NoSigs = true
	parts := strings.Split(eventsURL.Host, ":")
	nOpts.Host = parts[0]
	if len(parts) == 2 {
		nOpts.Port, err = strconv.Atoi(parts[1])
	} else {
		nOpts.Port = nats.DefaultPort
	}
	defer func() {
		if r := recover(); r != nil {
			e = nil
			if _, ok := r.(error); !ok {
				err = fmt.Errorf("failed to start NATS server: %v", r)
			} else {
				err = r.(error)
			}
		}
	}()
	s := stand.RunServerWithOpts(nil, nOpts)

	return s, nil
}

func getNATSPublisher(eventsAddress string) (*nats.EncodedConn, error) {
	nc, err := nats.Connect(eventsAddress)
	if err != nil {
		return nil, err
	}
	nec, err := nats.NewEncodedConn(nc, nats.JSON_ENCODER)
	if err != nil {
		nc.Close()
		return nil, err
	}

	return nec, nil
}
