// +build !windows

package main

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/linux/shim"
	shimapi "github.com/containerd/containerd/linux/shim/v1"
	"github.com/containerd/containerd/reaper"
	"github.com/containerd/typeurl"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
)

const shimUsage = `
                    __        _                     __           __    _
  _________  ____  / /_____ _(_)___  ___  _________/ /     _____/ /_  (_)___ ___
 / ___/ __ \/ __ \/ __/ __ ` + "`" + `/ / __ \/ _ \/ ___/ __  /_____/ ___/ __ \/ / __ ` + "`" + `__ \
/ /__/ /_/ / / / / /_/ /_/ / / / / /  __/ /  / /_/ /_____(__  ) / / / / / / / / /
\___/\____/_/ /_/\__/\__,_/_/_/ /_/\___/_/   \__,_/     /____/_/ /_/_/_/ /_/ /_/

shim for container lifecycle and reconnection
`

var shimCommand = cli.Command{
	Name:  "shim",
	Usage: shimUsage,
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "enable debug output in the logs",
		},
		cli.StringFlag{
			Name:  "namespace,n",
			Usage: "namespace that owns the task",
		},
		cli.StringFlag{
			Name:  "socket,s",
			Usage: "abstract socket path to serve on",
		},
		cli.StringFlag{
			Name:  "address,a",
			Usage: "grpc address back to containerd",
		},
		cli.StringFlag{
			Name:  "workdir,w",
			Usage: "path used to store large temporary data",
		},
		cli.StringFlag{
			Name:  "runtime-root",
			Usage: "root directory for the runtime",
			Value: shim.RuncRoot,
		},
		cli.StringFlag{
			Name:  "criu,c",
			Usage: "path to criu",
		},
		cli.BoolFlag{
			Name:  "systemd-cgroup",
			Usage: "set runtime to use systemd-cgroup",
		},
	},
	Before: func(context *cli.Context) error {
		if context.Bool("debug") {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	},
	Action: func(context *cli.Context) error {
		// start handling signals as soon as possible so that things are properly reaped
		// or if runtime exits before we hit the handler
		signals, err := setupSignals()
		if err != nil {
			return err
		}
		path, err := os.Getwd()
		if err != nil {
			return err
		}
		server := newServer()
		e, err := connectEvents(context.String("address"))
		if err != nil {
			return err
		}
		sv, err := shim.NewService(
			shim.Config{
				Path:          path,
				Namespace:     context.String("namespace"),
				WorkDir:       context.String("workdir"),
				Criu:          context.String("criu"),
				SystemdCgroup: context.Bool("systemd-cgroup"),
				RuntimeRoot:   context.String("runtime-root"),
			},
			&remoteEventsPublisher{client: e},
		)
		if err != nil {
			return err
		}
		shimapi.RegisterShimServer(server, sv)
		socket := context.String("socket")
		if err := serveShim(server, socket); err != nil {
			return err
		}
		logger := logrus.WithFields(logrus.Fields{
			"pid":       os.Getpid(),
			"path":      path,
			"namespace": context.String("namespace"),
		})
		return handleShimSignals(logger, signals, server, sv)
	},
}

// serveShim serves the grpc API over a unix socket at the provided path
// this function does not block
func serveShim(server *grpc.Server, path string) error {
	var (
		l   net.Listener
		err error
	)
	if path == "" {
		l, err = net.FileListener(os.NewFile(3, "socket"))
		path = "[inherited from parent]"
	} else {
		l, err = net.Listen("unix", "\x00"+path)
	}
	if err != nil {
		return err
	}
	logrus.WithField("socket", path).Debug("serving api on unix socket")
	go func() {
		defer l.Close()
		if err := server.Serve(l); err != nil &&
			!strings.Contains(err.Error(), "use of closed network connection") {
			logrus.WithError(err).Fatal("shim: GRPC server failure")
		}
	}()
	return nil
}

func handleShimSignals(logger *logrus.Entry, signals chan os.Signal, server *grpc.Server, sv *shim.Service) error {
	var (
		termOnce sync.Once
		done     = make(chan struct{})
	)

	for {
		select {
		case <-done:
			return nil
		case s := <-signals:
			switch s {
			case unix.SIGCHLD:
				if err := reaper.Reap(); err != nil {
					logger.WithError(err).Error("reap exit status")
				}
			case unix.SIGTERM, unix.SIGINT:
				go termOnce.Do(func() {
					server.Stop()
					// Ensure our child is dead if any
					sv.Kill(context.Background(), &shimapi.KillRequest{
						Signal: uint32(syscall.SIGKILL),
						All:    true,
					})
					sv.Delete(context.Background(), &google_protobuf.Empty{})
					close(done)
				})
			case unix.SIGUSR1:
				dumpStacks()
			}
		}
	}
}

func connectEvents(address string) (eventsapi.EventsClient, error) {
	conn, err := connect(address, containerd.Dialer)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial %q", address)
	}
	return eventsapi.NewEventsClient(conn), nil
}

func connect(address string, d func(string, time.Duration) (net.Conn, error)) (*grpc.ClientConn, error) {
	gopts := []grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithInsecure(),
		grpc.WithTimeout(60 * time.Second),
		grpc.WithDialer(d),
		grpc.FailOnNonTempDialError(true),
		grpc.WithBackoffMaxDelay(3 * time.Second),
	}
	conn, err := grpc.Dial(containerd.DialAddress(address), gopts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial %q", address)
	}
	return conn, nil
}

type remoteEventsPublisher struct {
	client eventsapi.EventsClient
}

func (l *remoteEventsPublisher) Publish(ctx context.Context, topic string, event events.Event) error {
	encoded, err := typeurl.MarshalAny(event)
	if err != nil {
		return err
	}
	if _, err := l.client.Publish(ctx, &eventsapi.PublishRequest{
		Topic: topic,
		Event: encoded,
	}); err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}
