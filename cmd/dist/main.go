package main

import (
	contextpkg "context"
	"fmt"
	"os"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd"
	"github.com/urfave/cli"
)

var (
	background = contextpkg.Background()
)

func init() {
	cli.VersionPrinter = func(c *cli.Context) {
		fmt.Println(c.App.Name, containerd.Package, c.App.Version)
	}

}

func main() {
	app := cli.NewApp()
	app.Name = "dist"
	app.Version = containerd.Version
	app.Usage = `
        ___      __
   ____/ (_)____/ /_
  / __  / / ___/ __/
 / /_/ / (__  ) /_
 \__,_/_/____/\__/

distribution tool
`
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "enable debug output in logs",
		},
		cli.DurationFlag{
			Name:  "timeout",
			Usage: "total timeout for dist commands",
		},
		cli.DurationFlag{
			Name:  "connect-timeout",
			Usage: "timeout for connecting to containerd",
		},
		cli.StringFlag{
			// TODO(stevvooe): for now, we allow circumventing the GRPC. Once
			// we have clear separation, this will likely go away.
			Name:  "root",
			Usage: "path to content store root",
			Value: "/var/lib/containerd",
		},
		cli.StringFlag{
			Name:  "socket, s",
			Usage: "socket path for containerd's GRPC server",
			Value: "/run/containerd/containerd.sock",
		},
	}
	app.Commands = []cli.Command{
		fetchCommand,
		ingestCommand,
		activeCommand,
		getCommand,
		deleteCommand,
		listCommand,
		applyCommand,
	}
	app.Before = func(context *cli.Context) error {
		var (
			debug   = context.GlobalBool("debug")
			timeout = context.GlobalDuration("timeout")
			cancel  contextpkg.CancelFunc
		)
		if debug {
			logrus.SetLevel(logrus.DebugLevel)
		}

		if timeout > 0 {
			background, cancel = contextpkg.WithTimeout(background, timeout)
			defer cancel()
		}
		return nil
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "dist: %s\n", err)
		os.Exit(1)
	}
}
