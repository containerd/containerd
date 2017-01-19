package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	"github.com/docker/containerd"
	"github.com/docker/containerd/api/shim"
	"github.com/docker/containerd/sys"
	"github.com/docker/containerd/utils"
	"github.com/sirupsen/logrus"
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
		var (
			server = grpc.NewServer()
			sv     = &service{}
		)
		shim.RegisterShimServiceServer(server, sv)
		l, err := utils.CreateUnixSocket("shim.sock")
		if err != nil {
			return err
		}
		go func() {
			defer l.Close()
			if err := server.Serve(l); err != nil {
				l.Close()
				logrus.WithError(err).Fatal("containerd-shim: GRPC server failure")
			}
		}()
		for s := range signals {
			switch s {
			case syscall.SIGCHLD:
				exits, err := utils.Reap(false)
				if err != nil {
					logrus.WithError(err).Error("reap exit status")
				}
				for _, e := range exits {
					if err := sv.processExited(e); err != nil {
						return err
					}
				}
			case syscall.SIGTERM, syscall.SIGINT:
				server.GracefulStop()
				return nil
			}
		}
		return nil
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
