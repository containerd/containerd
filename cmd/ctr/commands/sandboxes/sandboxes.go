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

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/urfave/cli/v2"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/oci"
)

// Command is a set of subcommands to manage runtimes with sandbox support
var Command = &cli.Command{
	Name:    "sandboxes",
	Aliases: []string{"sandbox", "sb", "s"},
	Usage:   "Manage sandboxes",
	Subcommands: cli.Commands{
		runCommand,
		listCommand,
		removeCommand,
		infoCommand,
	},
}

var runCommand = &cli.Command{
	Name:      "run",
	Aliases:   []string{"create", "c", "r"},
	Usage:     "Run a new sandbox",
	ArgsUsage: "[flags] <pod-config.json> <sandbox-id>",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "runtime",
			Usage: "Runtime name",
			Value: defaults.DefaultRuntime,
		},
	},
	Action: func(cliContext *cli.Context) error {
		if cliContext.NArg() != 2 {
			return cli.ShowSubcommandHelp(cliContext)
		}
		var (
			id      = cliContext.Args().Get(1)
			runtime = cliContext.String("runtime")
		)

		spec, err := os.ReadFile(cliContext.Args().First())
		if err != nil {
			return fmt.Errorf("failed to read sandbox config: %w", err)
		}

		ociSpec := oci.Spec{}
		if err = json.Unmarshal(spec, &ociSpec); err != nil {
			return fmt.Errorf("failed to parse sandbox config: %w", err)
		}

		client, ctx, cancel, err := commands.NewClient(cliContext)
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

var listCommand = &cli.Command{
	Name:    "list",
	Aliases: []string{"ls"},
	Usage:   "List sandboxes",
	Flags: []cli.Flag{
		&cli.StringSliceFlag{
			Name:  "filters",
			Usage: "The list of filters to apply when querying sandboxes from the store",
		},
	},
	Action: func(cliContext *cli.Context) error {
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		var (
			writer  = tabwriter.NewWriter(os.Stdout, 1, 8, 1, ' ', 0)
			filters = cliContext.StringSlice("filters")
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

var removeCommand = &cli.Command{
	Name:      "remove",
	Aliases:   []string{"rm"},
	ArgsUsage: "<id> [<id>, ...]",
	Usage:     "Remove sandboxes",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:    "force",
			Aliases: []string{"f"},
			Usage:   "Ignore shutdown errors when removing sandbox",
		},
	},
	Action: func(cliContext *cli.Context) error {
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		force := cliContext.Bool("force")

		for _, id := range cliContext.Args().Slice() {
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

var infoCommand = &cli.Command{
	Name:      "info",
	Aliases:   []string{"i"},
	Usage:     "Get info about a sandbox",
	ArgsUsage: "<sandbox id>",
	Action: func(cliContext *cli.Context) error {
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		id := cliContext.Args().First()
		if id == "" {
			return fmt.Errorf("sandbox id must be provided: %w", errdefs.ErrInvalidArgument)
		}
		sandbox, err := client.LoadSandbox(ctx, id)
		if err != nil {
			return fmt.Errorf("failed to load sandbox %s", id)
		}

		info := sandbox.Metadata()
		commands.PrintAsJSON(info)
		return nil
	},
}
