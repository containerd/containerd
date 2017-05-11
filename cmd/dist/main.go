package main

import (
	contextpkg "context"
	"fmt"
	"os"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/containerd/version"
	"github.com/urfave/cli"
)

var (
	timeout time.Duration
)

func init() {
	cli.VersionPrinter = func(c *cli.Context) {
		fmt.Println(c.App.Name, version.Package, c.App.Version)
	}

}

func appContext() (contextpkg.Context, contextpkg.CancelFunc) {
	background := contextpkg.Background()
	if timeout > 0 {
		return contextpkg.WithTimeout(background, timeout)
	}
	return contextpkg.WithCancel(background)
}

func main() {
	app := cli.NewApp()
	app.Name = "dist"
	app.Version = version.Version
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
			Name:  "address, a",
			Usage: "address for containerd's GRPC server",
			Value: "/run/containerd/containerd.sock",
		},
	}
	app.Commands = []cli.Command{
		imageCommand,
		contentCommand,
		pullCommand,
		fetchCommand,
		fetchObjectCommand,
		applyCommand,
		rootfsCommand,
	}
	app.Before = func(context *cli.Context) error {
		timeout = context.GlobalDuration("timeout")
		if context.GlobalBool("debug") {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "dist: %s\n", err)
		os.Exit(1)
	}
}

var imageCommand = cli.Command{
	Name:  "image",
	Usage: "image management",
	Subcommands: cli.Commands{
		imagesListCommand,
		rmiCommand,
	},
}

var contentCommand = cli.Command{
	Name:  "content",
	Usage: "content management",
	Subcommands: cli.Commands{
		listCommand,
		ingestCommand,
		activeCommand,
		getCommand,
		editCommand,
		deleteCommand,
	},
}
