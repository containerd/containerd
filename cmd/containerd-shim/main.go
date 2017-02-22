package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"google.golang.org/grpc"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd"
	shimapi "github.com/docker/containerd/api/services/shim"
	"github.com/docker/containerd/linux/shim"
	"github.com/docker/containerd/sys"
	"github.com/docker/containerd/utils"
	"github.com/urfave/cli"
)

const usage = `
                    __        _                     __           __    _         
  _________  ____  / /_____ _(_)___  ___  _________/ /     _____/ /_  (_)___ ___ 
 / ___/ __ \/ __ \/ __/ __ ` + "`" + `/ / __ \/ _ \/ ___/ __  /_____/ ___/ __ \/ / __ ` + "`" + `__ \
/ /__/ /_/ / / / / /_/ /_/ / / / / /  __/ /  / /_/ /_____(__  ) / / / / / / / / /
\___/\____/_/ /_/\__/\__,_/_/_/ /_/\___/_/   \__,_/     /____/_/ /_/_/_/ /_/ /_/ 
                                                                                 
shim for container lifecycle and reconnection
`

func main() {
	app := cli.NewApp()
	app.Name = "containerd-shim"
	app.Version = containerd.Version
	app.Usage = usage
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "enable debug output in logs",
		},
	}
	app.Before = func(context *cli.Context) error {
		if context.GlobalBool("debug") {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	}
	app.Action = func(context *cli.Context) error {
		// start handling signals as soon as possible so that things are properly reaped
		// or if runtime exits before we hit the handler
		signals, err := setupSignals()
		if err != nil {
			return err
		}
		if err := setupRoot(); err != nil {
			return err
		}
		var (
			server = grpc.NewServer()
			sv     = shim.New()
		)
		logrus.Debug("registering grpc server")
		shimapi.RegisterShimServer(server, sv)
		if err := serve(server, "shim.sock"); err != nil {
			return err
		}
		return handleSignals(signals, server, sv)
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "containerd-shim: %s\n", err)
		os.Exit(1)
	}
}

// setupSignals creates a new signal handler for all signals and sets the shim as a
// sub-reaper so that the container processes are reparented
func setupSignals() (chan os.Signal, error) {
	signals := make(chan os.Signal, 2048)
	signal.Notify(signals)
	// set the shim as the subreaper for all orphaned processes created by the container
	if err := sys.SetSubreaper(1); err != nil {
		return nil, err
	}
	return signals, nil
}

// serve serves the grpc API over a unix socket at the provided path
// this function does not block
func serve(server *grpc.Server, path string) error {
	l, err := net.FileListener(os.NewFile(3, "socket"))
	if err != nil {
		return err
	}
	logrus.WithField("socket", path).Debug("serving api on unix socket")
	go func() {
		defer l.Close()
		if err := server.Serve(l); err != nil &&
			!strings.Contains(err.Error(), "use of closed network connection") {
			logrus.WithError(err).Fatal("containerd-shim: GRPC server failure")
		}
	}()
	return nil
}

func handleSignals(signals chan os.Signal, server *grpc.Server, service *shim.Service) error {
	for s := range signals {
		logrus.WithField("signal", s).Debug("received signal")
		switch s {
		case syscall.SIGCHLD:
			exits, err := utils.Reap(false)
			if err != nil {
				logrus.WithError(err).Error("reap exit status")
			}
			for _, e := range exits {
				logrus.WithFields(logrus.Fields{
					"status": e.Status,
					"pid":    e.Pid,
				}).Debug("process exited")
				if err := service.ProcessExit(e); err != nil {
					return err
				}
			}
		case syscall.SIGTERM, syscall.SIGINT:
			// TODO: should we forward signals to the processes if they are still running?
			// i.e. machine reboot
			server.Stop()
			return nil
		}
	}
	return nil
}

// setupRoot sets up the root as the shim is started in its own mount namespace
func setupRoot() error {
	return syscall.Mount("", "/", "", syscall.MS_SLAVE|syscall.MS_REC, "")
}
