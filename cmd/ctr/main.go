package main

import (
	"fmt"
	"os"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/containerd/version"
	"github.com/urfave/cli"
)

var (
	extraCmds = []cli.Command{}
)

func init() {
	cli.VersionPrinter = func(c *cli.Context) {
		fmt.Println(c.App.Name, version.Package, c.App.Version)
	}
}

func main() {
	app := cli.NewApp()
	app.Name = "ctr"
	app.Version = version.Version
	app.Usage = `
        __
  _____/ /______
 / ___/ __/ ___/
/ /__/ /_/ /
\___/\__/_/

containerd client
`
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "enable debug output in logs",
		},
		cli.StringFlag{
			Name:  "address, a",
			Usage: "address for containerd's GRPC server",
			Value: "/run/containerd/containerd.sock",
		},
		cli.StringFlag{
			// TODO(stevvooe): for now, we allow circumventing the GRPC. Once
			// we have clear separation, this will likely go away.
			Name:  "root",
			Usage: "path to content store root",
			Value: "/var/lib/containerd",
		},
	}
	app.Commands = []cli.Command{
		checkpointCommand,
		runCommand,
		eventsCommand,
		deleteCommand,
		listCommand,
		infoCommand,
		killCommand,
		pprofCommand,
		execCommand,
		pauseCommand,
		resumeCommand,
		snapshotCommand,
		versionCommand,
		psCommand,
	}
	app.Commands = append(app.Commands, extraCmds...)
	app.Before = func(context *cli.Context) error {
		if context.GlobalBool("debug") {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "ctr: %s\n", err)
		os.Exit(1)
	}
}
