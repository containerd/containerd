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

package sandboxes

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/defaults"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/oci"
	"github.com/urfave/cli"
)

// Command is a set of subcommands to manage runtimes with sandbox support
var Command = cli.Command{
	Name:    "sandboxes",
	Aliases: []string{"sandbox", "sb", "s"},
	Usage:   "manage sandboxes",
	Subcommands: cli.Commands{
		runCommand,
		listCommand,
		removeCommand,
	},
}

var runCommand = cli.Command{
	Name:      "run",
	Aliases:   []string{"create", "c", "r"},
	Usage:     "run a new sandbox",
	ArgsUsage: "[flags] <pod-config.json> <sandbox-id>",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "runtime",
			Usage: "Runtime name",
			Value: defaults.DefaultRuntime,
		},
	},
	Action: func(context *cli.Context) error {
		if context.NArg() != 2 {
			return cli.ShowSubcommandHelp(context)
		}
		var (
			id      = context.Args().Get(1)
			runtime = context.String("runtime")
		)

		spec, err := os.ReadFile(context.Args().First())
		if err != nil {
			return fmt.Errorf("failed to read sandbox config: %w", err)
		}

		ociSpec := oci.Spec{}
		if err = json.Unmarshal(spec, &ociSpec); err != nil {
			return fmt.Errorf("failed to parse sandbox config: %w", err)
		}

		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		sandbox, err := client.NewSandbox(ctx, id,
			containerd.WithSandboxRuntime(runtime, nil),
			containerd.WithSandboxSpec(&ociSpec),
		)
		if err != nil {
			return fmt.Errorf("failed to create new sandbox: %w", err)
		}

		err = sandbox.Start(ctx)
		if err != nil {
			return fmt.Errorf("failed to start: %w", err)
		}

		fmt.Println(sandbox.ID())
		return nil
	},
}

var listCommand = cli.Command{
	Name:    "list",
	Aliases: []string{"ls"},
	Usage:   "list sandboxes",
	Flags: []cli.Flag{
		cli.StringSliceFlag{
			Name:  "filters",
			Usage: "The list of filters to apply when querying sandboxes from the store",
		},
	},
	Action: func(context *cli.Context) error {
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		var (
			writer  = tabwriter.NewWriter(os.Stdout, 1, 8, 1, ' ', 0)
			filters = context.StringSlice("filters")
		)

		defer func() {
			_ = writer.Flush()
		}()

		list, err := client.SandboxStore().List(ctx, filters...)
		if err != nil {
			return fmt.Errorf("failed to list sandboxes: %w", err)
		}

		if _, err := fmt.Fprintln(writer, "ID\tCREATED\tRUNTIME\t"); err != nil {
			return err
		}

		for _, sandbox := range list {
			_, err := fmt.Fprintf(writer, "%s\t%s\t%s\t\n", sandbox.ID, sandbox.CreatedAt, sandbox.Runtime.Name)
			if err != nil {
				return err
			}
		}

		return nil
	},
}

var removeCommand = cli.Command{
	Name:      "remove",
	Aliases:   []string{"rm"},
	ArgsUsage: "<id> [<id>, ...]",
	Usage:     "remove sandboxes",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "force, f",
			Usage: "Ignore shutdown errors when removing sandbox",
		},
	},
	Action: func(context *cli.Context) error {
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		force := context.Bool("force")

		for _, id := range context.Args() {
			sandbox, err := client.LoadSandbox(ctx, id)
			if err != nil {
				log.G(ctx).WithError(err).Errorf("failed to load sandbox %s", id)
				continue
			}

			err = sandbox.Stop(ctx)
			if err != nil {
				log.G(ctx).WithError(err).Errorf("failed to stop sandbox %s", id)
				if !force {
					continue
				}
			}

			err = sandbox.Shutdown(ctx)
			if err != nil {
				log.G(ctx).WithError(err).Errorf("failed to shutdown sandbox %s", id)
				continue
			}

			log.G(ctx).Infof("deleted: %s", id)
		}

		return nil
	},
}
