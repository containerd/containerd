package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"

	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/server"
	"github.com/containerd/containerd/version"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"google.golang.org/grpc/grpclog"
)

var extraCmds = []cli.Command{}

func init() {
	// Discard grpc logs so that they don't mess with our stdio
	grpclog.SetLogger(log.New(ioutil.Discard, "", log.LstdFlags))

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
		containersCommand,
		contentCommand,
		eventsCommand,
		fetchCommand,
		fetchObjectCommand,
		imageCommand,
		namespacesCommand,
		pprofCommand,
		pullCommand,
		pushCommand,
		pushObjectCommand,
		rootfsCommand,
		runCommand,
		snapshotCommand,
		tasksCommand,
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
