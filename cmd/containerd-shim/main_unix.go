// +build !windows

package main

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"

	"google.golang.org/grpc"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/containerd/linux/shim"
	shimapi "github.com/containerd/containerd/linux/shim/v1"
	"github.com/containerd/containerd/reaper"
	"github.com/containerd/containerd/version"
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
	app.Version = version.Version
	app.Usage = usage
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "enable debug output in logs",
		},
		cli.StringFlag{
			Name:  "namespace,n",
			Usage: "namespace that owns the task",
		},
		cli.StringFlag{
			Name:  "socket,s",
			Usage: "abstract socket path to serve on",
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
		path, err := os.Getwd()
		if err != nil {
			return err
		}
		server := grpc.NewServer()
		sv, err := shim.NewService(
			path,
			context.GlobalString("namespace"),
		)
		if err != nil {
			return err
		}
		logrus.Debug("registering grpc server")
		shimapi.RegisterShimServer(server, sv)
		socket := context.GlobalString("socket")
		if err := serve(server, socket); err != nil {
			return err
		}
		return handleSignals(signals, server)
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "containerd-shim: %s\n", err)
		os.Exit(1)
	}
}

// serve serves the grpc API over a unix socket at the provided path
// this function does not block
func serve(server *grpc.Server, path string) error {
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
			logrus.WithError(err).Fatal("containerd-shim: GRPC server failure")
		}
	}()
	return nil
}

func handleSignals(signals chan os.Signal, server *grpc.Server) error {
	for s := range signals {
		logrus.WithField("signal", s).Debug("received signal")
		switch s {
		case unix.SIGCHLD:
			if err := reaper.Reap(); err != nil {
				logrus.WithError(err).Error("reap exit status")
			}
		case unix.SIGTERM, unix.SIGINT:
			// TODO: should we forward signals to the processes if they are still running?
			// i.e. machine reboot
			server.Stop()
			return nil
		case unix.SIGUSR1:
			dumpStacks()
		}
	}
	return nil
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
