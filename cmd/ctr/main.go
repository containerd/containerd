package main

import (
	"fmt"
	"os"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/containerd/version"
	"github.com/urfave/cli"
)

var extraCmds = []cli.Command{}

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

containerd CLI
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
		cli.DurationFlag{
			Name:  "timeout",
			Usage: "total timeout for ctr commands",
		},
		cli.StringFlag{
			Name:   "namespace, n",
			Usage:  "namespace to use with commands",
			Value:  "default",
			EnvVar: "CONTAINERD_NAMESPACE",
		},
	}
	app.Commands = append([]cli.Command{
		attachCommand,
		checkpointCommand,
		runCommand,
		deleteCommand,
		namespacesCommand,
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
	}, extraCmds...)
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
