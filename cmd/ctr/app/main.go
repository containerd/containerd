/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package app

import (
	"fmt"
	"io"

	"github.com/containerd/log"
	"github.com/urfave/cli"
	"google.golang.org/grpc/grpclog"

	"github.com/containerd/containerd/v2/cmd/ctr/commands/containers"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/content"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/deprecations"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/events"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/images"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/info"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/install"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/leases"
	namespacesCmd "github.com/containerd/containerd/v2/cmd/ctr/commands/namespaces"
	ociCmd "github.com/containerd/containerd/v2/cmd/ctr/commands/oci"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/plugins"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/pprof"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/run"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/sandboxes"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/snapshots"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/tasks"
	versionCmd "github.com/containerd/containerd/v2/cmd/ctr/commands/version"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/namespaces"
	"github.com/containerd/containerd/v2/version"
)

var extraCmds = []cli.Command{}

func init() {
	// Discard grpc logs so that they don't mess with our stdio
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(io.Discard, io.Discard, io.Discard))

	cli.VersionPrinter = func(c *cli.Context) {
		fmt.Println(c.App.Name, version.Package, c.App.Version)
	}
	cli.VersionFlag = cli.BoolFlag{
		Name:  "version, v",
		Usage: "Print the version",
	}
	cli.HelpFlag = cli.BoolFlag{
		Name:  "help, h",
		Usage: "Show help",
	}
}

// New returns a *cli.App instance.
func New() *cli.App {
	app := cli.NewApp()
	app.Name = "ctr"
	app.Version = version.Version
	app.Description = `
ctr is an unsupported debug and administrative client for interacting
with the containerd daemon. Because it is unsupported, the commands,
options, and operations are not guaranteed to be backward compatible or
stable from release to release of the containerd project.`
	app.Usage = `
        __
  _____/ /______
 / ___/ __/ ___/
/ /__/ /_/ /
\___/\__/_/

containerd CLI
`
	app.EnableBashCompletion = true
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "Enable debug output in logs",
		},
		cli.StringFlag{
			Name:   "address, a",
			Usage:  "Address for containerd's GRPC server",
			Value:  defaults.DefaultAddress,
			EnvVar: "CONTAINERD_ADDRESS",
		},
		cli.DurationFlag{
			Name:  "timeout",
			Usage: "Total timeout for ctr commands",
		},
		cli.DurationFlag{
			Name:  "connect-timeout",
			Usage: "Timeout for connecting to containerd",
		},
		cli.StringFlag{
			Name:   "namespace, n",
			Usage:  "Namespace to use with commands",
			Value:  namespaces.Default,
			EnvVar: namespaces.NamespaceEnvVar,
		},
	}
	app.Commands = append([]cli.Command{
		plugins.Command,
		versionCmd.Command,
		containers.Command,
		content.Command,
		events.Command,
		images.Command,
		leases.Command,
		namespacesCmd.Command,
		pprof.Command,
		run.Command,
		snapshots.Command,
		tasks.Command,
		install.Command,
		ociCmd.Command,
		sandboxes.Command,
		info.Command,
		deprecations.Command,
	}, extraCmds...)
	app.Before = func(context *cli.Context) error {
		if context.GlobalBool("debug") {
			return log.SetLevel("debug")
		}
		return nil
	}
	return app
}
