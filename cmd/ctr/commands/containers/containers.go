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

package containers

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/run"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	"github.com/urfave/cli/v2"
)

// Command is the cli command for managing containers
var Command = &cli.Command{
	Name:    "containers",
	Usage:   "Manage containers",
	Aliases: []string{"c", "container"},
	Subcommands: []*cli.Command{
		createCommand,
		deleteCommand,
		infoCommand,
		listCommand,
		setLabelsCommand,
		checkpointCommand,
		restoreCommand,
	},
}

var createCommand = &cli.Command{
	Name:      "create",
	Usage:     "Create container",
	ArgsUsage: "[flags] Image|RootFS CONTAINER [COMMAND] [ARG...]",
	Flags:     append(commands.RuntimeFlags, append(append(commands.SnapshotterFlags, []cli.Flag{commands.SnapshotterLabels}...), commands.ContainerFlags...)...),
	Action: func(cliContext *cli.Context) error {
		var (
			id     string
			ref    string
			config = cliContext.IsSet("config")
		)

		if config {
			id = cliContext.Args().First()
			if cliContext.NArg() > 1 {
				return fmt.Errorf("with spec config file, only container id should be provided: %w", errdefs.ErrInvalidArgument)
			}
		} else {
			id = cliContext.Args().Get(1)
			ref = cliContext.Args().First()
			if ref == "" {
				return fmt.Errorf("image ref must be provided: %w", errdefs.ErrInvalidArgument)
			}
		}
		if id == "" {
			return fmt.Errorf("container id must be provided: %w", errdefs.ErrInvalidArgument)
		}
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()
		_, err = run.NewContainer(ctx, client, cliContext)
		if err != nil {
			return err
		}
		return nil
	},
}

var listCommand = &cli.Command{
	Name:      "list",
	Aliases:   []string{"ls"},
	Usage:     "List containers",
	ArgsUsage: "[flags] [<filter>, ...]",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:    "quiet",
			Aliases: []string{"q"},
			Usage:   "Print only the container id",
		},
	},
	Action: func(cliContext *cli.Context) error {
		var (
			filters = cliContext.Args().Slice()
			quiet   = cliContext.Bool("quiet")
		)
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()
		containers, err := client.Containers(ctx, filters...)
		if err != nil {
			return err
		}
		if quiet {
			for _, c := range containers {
				fmt.Printf("%s\n", c.ID())
			}
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 4, 8, 4, ' ', 0)
		fmt.Fprintln(w, "CONTAINER\tIMAGE\tRUNTIME\t")
		for _, c := range containers {
			info, err := c.Info(ctx, containerd.WithoutRefreshedMetadata)
			if err != nil {
				return err
			}
			imageName := info.Image
			if imageName == "" {
				imageName = "-"
			}
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t\n",
				c.ID(),
				imageName,
				info.Runtime.Name,
			); err != nil {
				return err
			}
		}
		return w.Flush()
	},
}

var deleteCommand = &cli.Command{
	Name:      "delete",
	Usage:     "Delete one or more existing containers",
	ArgsUsage: "[flags] CONTAINER [CONTAINER, ...]",
	Aliases:   []string{"del", "remove", "rm"},
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "keep-snapshot",
			Usage: "Do not clean up snapshot with container",
		},
	},
	Action: func(cliContext *cli.Context) error {
		var exitErr error
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()
		deleteOpts := []containerd.DeleteOpts{}
		if !cliContext.Bool("keep-snapshot") {
			deleteOpts = append(deleteOpts, containerd.WithSnapshotCleanup)
		}

		if cliContext.NArg() == 0 {
			return fmt.Errorf("must specify at least one container to delete: %w", errdefs.ErrInvalidArgument)
		}
		for _, arg := range cliContext.Args().Slice() {
			if err := deleteContainer(ctx, client, arg, deleteOpts...); err != nil {
				if exitErr == nil {
					exitErr = err
				}
				log.G(ctx).WithError(err).Errorf("failed to delete container %q", arg)
			}
		}
		return exitErr
	},
}

func deleteContainer(ctx context.Context, client *containerd.Client, id string, opts ...containerd.DeleteOpts) error {
	container, err := client.LoadContainer(ctx, id)
	if err != nil {
		return err
	}
	task, err := container.Task(ctx, cio.Load)
	if err != nil {
		return container.Delete(ctx, opts...)
	}
	status, err := task.Status(ctx)
	if err != nil {
		return err
	}
	if status.Status == containerd.Stopped || status.Status == containerd.Created {
		if _, err := task.Delete(ctx); err != nil {
			return err
		}
		return container.Delete(ctx, opts...)
	}
	return fmt.Errorf("cannot delete a non stopped container: %v", status)

}

var setLabelsCommand = &cli.Command{
	Name:        "label",
	Usage:       "Set and clear labels for a container",
	ArgsUsage:   "[flags] CONTAINER [<key>=<value>, ...]",
	Description: "set and clear labels for a container",
	Flags:       []cli.Flag{},
	Action: func(cliContext *cli.Context) error {
		containerID, labels := commands.ObjectWithLabelArgs(cliContext)
		if containerID == "" {
			return fmt.Errorf("container id must be provided: %w", errdefs.ErrInvalidArgument)
		}
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		container, err := client.LoadContainer(ctx, containerID)
		if err != nil {
			return err
		}

		setlabels, err := container.SetLabels(ctx, labels)
		if err != nil {
			return err
		}

		var labelStrings []string
		for k, v := range setlabels {
			labelStrings = append(labelStrings, fmt.Sprintf("%s=%s", k, v))
		}

		fmt.Println(strings.Join(labelStrings, ","))

		return nil
	},
}

var infoCommand = &cli.Command{
	Name:      "info",
	Usage:     "Get info about a container",
	ArgsUsage: "CONTAINER",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "spec",
			Usage: "Only display the spec",
		},
	},
	Action: func(cliContext *cli.Context) error {
		id := cliContext.Args().First()
		if id == "" {
			return fmt.Errorf("container id must be provided: %w", errdefs.ErrInvalidArgument)
		}
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()
		container, err := client.LoadContainer(ctx, id)
		if err != nil {
			return err
		}
		info, err := container.Info(ctx, containerd.WithoutRefreshedMetadata)
		if err != nil {
			return err
		}
		if cliContext.Bool("spec") {
			v, err := typeurl.UnmarshalAny(info.Spec)
			if err != nil {
				return err
			}
			commands.PrintAsJSON(v)
			return nil
		}

		if info.Spec != nil && info.Spec.GetValue() != nil {
			v, err := typeurl.UnmarshalAny(info.Spec)
			if err != nil {
				return err
			}
			commands.PrintAsJSON(struct {
				containers.Container
				Spec interface{} `json:"Spec,omitempty"`
			}{
				Container: info,
				Spec:      v,
			})
			return nil
		}
		commands.PrintAsJSON(info)
		return nil
	},
}
