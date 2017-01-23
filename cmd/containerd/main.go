package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"

	gocontext "golang.org/x/net/context"
	"google.golang.org/grpc"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd"
	api "github.com/docker/containerd/api/execution"
	"github.com/docker/containerd/events"
	"github.com/docker/containerd/execution"
	"github.com/docker/containerd/log"
	metrics "github.com/docker/go-metrics"
	"github.com/urfave/cli"

	"github.com/nats-io/go-nats"
	stand "github.com/nats-io/nats-streaming-server/server"
)

func main() {
	app := cli.NewApp()
	app.Name = "containerd"
	app.Version = containerd.Version
	app.Usage = `
                    __        _                     __
  _________  ____  / /_____ _(_)___  ___  _________/ /
 / ___/ __ \/ __ \/ __/ __ ` + "`" + `/ / __ \/ _ \/ ___/ __  /
/ /__/ /_/ / / / / /_/ /_/ / / / / /  __/ /  / /_/ /
\___/\____/_/ /_/\__/\__,_/_/_/ /_/\___/_/   \__,_/

high performance container runtime
`
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
	app.Flags = appendPlatformFlags(app.Flags)

	app.Before = func(context *cli.Context) error {
		if context.GlobalBool("debug") {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	}
	app.Action = func(context *cli.Context) error {
		signals := make(chan os.Signal, 2048)
		setupSignals(signals)

		if address := context.GlobalString("metrics-address"); address != "" {
			go serveMetrics(address)
		}

		s, err := startNATSServer(context)
		if err != nil {
			return nil
		}
		defer s.Shutdown()

		path := context.GlobalString("socket")
		l, err := createListener(path)
		if err != nil {
			return err
		}

		// Get events publisher
		nec, err := getNATSPublisher(context)
		if err != nil {
			return err
		}
		defer nec.Close()
		ctx := log.WithModule(gocontext.Background(), "containerd")
		ctx = log.WithModule(ctx, "execution")
		ctx = events.WithPoster(ctx, events.GetNATSPoster(nec))

		var (
			executor execution.Executor
			runtime  = context.GlobalString("runtime")
		)
		executor, err = processRuntime(ctx, runtime, context.GlobalString("root"))
		if err != nil {
			return err
		}

		execService, err := execution.New(ctx, executor)
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
		go serveGRPC(server, l)

		handleSignals(signals, server)
		return nil
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "containerd: %s\n", err)
		os.Exit(1)
	}
}

func serveMetrics(address string) {
	m := http.NewServeMux()
	m.Handle("/metrics", metrics.Handler())
	if err := http.ListenAndServe(address, m); err != nil {
		logrus.WithError(err).Fatal("containerd: metrics server failure")
	}
}

func serveGRPC(server *grpc.Server, l net.Listener) {
	defer l.Close()
	if err := server.Serve(l); err != nil {
		l.Close()
		logrus.WithError(err).Fatal("containerd: GRPC server failure")
	}
}

// DumpStacks dumps the runtime stack.
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

func startNATSServer(context *cli.Context) (e *stand.StanServer, err error) {
	eventsURL, err := url.Parse(context.GlobalString("events-address"))
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

func getNATSPublisher(context *cli.Context) (*nats.EncodedConn, error) {
	nc, err := nats.Connect(context.GlobalString("events-address"))
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
