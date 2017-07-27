package main

import (
	"fmt"
	"os"

	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/server"
	"github.com/containerd/containerd/version"
	"github.com/sirupsen/logrus"
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
			Value: server.DefaultAddress,
		},
		cli.DurationFlag{
			Name:  "timeout",
			Usage: "total timeout for ctr commands",
		},
		cli.DurationFlag{
			Name:  "connect-timeout",
			Usage: "timeout for connecting to containerd",
		},
		cli.StringFlag{
			Name:   "namespace, n",
			Usage:  "namespace to use with commands",
			Value:  namespaces.Default,
			EnvVar: namespaces.NamespaceEnvVar,
		},
	}
	app.Commands = append([]cli.Command{
		applyCommand,
		attachCommand,
		checkpointCommand,
		containersCommand,
		contentCommand,
		eventsCommand,
		execCommand,
		fetchCommand,
		fetchObjectCommand,
		imageCommand,
		infoCommand,
		killCommand,
		namespacesCommand,
		pauseCommand,
		pprofCommand,
		psCommand,
		pullCommand,
		pushCommand,
		pushObjectCommand,
		resumeCommand,
		rootfsCommand,
		runCommand,
		snapshotCommand,
		taskListCommand,
		versionCommand,
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

var containersCommand = cli.Command{
	Name:    "containers",
	Usage:   "manage containers (metadata)",
	Aliases: []string{"c"},
	Subcommands: []cli.Command{
		containersListCommand,
		containersDeleteCommand,
		containersSetLabelsCommand,
	},
}
