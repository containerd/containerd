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

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/cmd/ctr/commands/run"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/typeurl"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

// Command is the cli command for managing containers
var Command = cli.Command{
	Name:    "containers",
	Usage:   "manage containers",
	Aliases: []string{"c", "container"},
	Subcommands: []cli.Command{
		createCommand,
		deleteCommand,
		infoCommand,
		listCommand,
		setLabelsCommand,
		checkpointCommand,
		restoreCommand,
	},
}

var createCommand = cli.Command{
	Name:      "create",
	Usage:     "create container",
	ArgsUsage: "[flags] Image|RootFS CONTAINER [COMMAND] [ARG...]",
	Flags:     append(commands.SnapshotterFlags, commands.ContainerFlags...),
	Action: func(context *cli.Context) error {
		var (
			id     string
			ref    string
			config = context.IsSet("config")
		)

		if config {
			id = context.Args().First()
			if context.NArg() > 1 {
				return errors.New("with spec config file, only container id should be provided")
			}
		} else {
			id = context.Args().Get(1)
			ref = context.Args().First()
			if ref == "" {
				return errors.New("image ref must be provided")
			}
		}
		if id == "" {
			return errors.New("container id must be provided")
		}
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		_, err = run.NewContainer(ctx, client, context)
		if err != nil {
			return err
		}
		return nil
	},
}

var listCommand = cli.Command{
	Name:      "list",
	Aliases:   []string{"ls"},
	Usage:     "list containers",
	ArgsUsage: "[flags] [<filter>, ...]",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "quiet, q",
			Usage: "print only the container id",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			filters = context.Args()
			quiet   = context.Bool("quiet")
		)
		client, ctx, cancel, err := commands.NewClient(context)
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
			info, err := c.Info(ctx)
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

var deleteCommand = cli.Command{
	Name:      "delete",
	Usage:     "delete one or more existing containers",
	ArgsUsage: "[flags] CONTAINER [CONTAINER, ...]",
	Aliases:   []string{"del", "rm"},
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "keep-snapshot",
			Usage: "do not clean up snapshot with container",
		},
	},
	Action: func(context *cli.Context) error {
		var exitErr error
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		deleteOpts := []containerd.DeleteOpts{}
		if !context.Bool("keep-snapshot") {
			deleteOpts = append(deleteOpts, containerd.WithSnapshotCleanup)
		}

		if context.NArg() == 0 {
			return errors.New("must specify at least one container to delete")
		}
		for _, arg := range context.Args() {
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

var setLabelsCommand = cli.Command{
	Name:        "label",
	Usage:       "set and clear labels for a container",
	ArgsUsage:   "[flags] CONTAINER [<key>=<value>, ...]",
	Description: "set and clear labels for a container",
	Flags:       []cli.Flag{},
	Action: func(context *cli.Context) error {
		containerID, labels := commands.ObjectWithLabelArgs(context)
		if containerID == "" {
			return errors.New("container id must be provided")
		}
		client, ctx, cancel, err := commands.NewClient(context)
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

var infoCommand = cli.Command{
	Name:      "info",
	Usage:     "get info about a container",
	ArgsUsage: "CONTAINER",
	Action: func(context *cli.Context) error {
		id := context.Args().First()
		if id == "" {
			return errors.New("container id must be provided")
		}
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		container, err := client.LoadContainer(ctx, id)
		if err != nil {
			return err
		}
		info, err := container.Info(ctx)
		if err != nil {
			return err
		}

		if info.Spec != nil && info.Spec.Value != nil {
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

var checkpointCommand = cli.Command{
	Name:      "checkpoint",
	Usage:     "checkpoint a container",
	ArgsUsage: "CONTAINER REF",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "rw",
			Usage: "include the rw layer in the checkpoint",
		},
		cli.BoolFlag{
			Name:  "image",
			Usage: "include the image in the checkpoint",
		},
		cli.BoolFlag{
			Name:  "task",
			Usage: "checkpoint container task",
		},
	},
	Action: func(context *cli.Context) error {
		id := context.Args().First()
		if id == "" {
			return errors.New("container id must be provided")
		}
		ref := context.Args().Get(1)
		if ref == "" {
			return errors.New("ref must be provided")
		}
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		opts := []containerd.CheckpointOpts{
			containerd.WithCheckpointRuntime,
		}

		if context.Bool("image") {
			opts = append(opts, containerd.WithCheckpointImage)
		}
		if context.Bool("rw") {
			opts = append(opts, containerd.WithCheckpointRW)
		}
		if context.Bool("task") {
			opts = append(opts, containerd.WithCheckpointTask)
		}
		container, err := client.LoadContainer(ctx, id)
		if err != nil {
			return err
		}
		task, err := container.Task(ctx, nil)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				return err
			}
		}
		// pause if running
		if task != nil {
			if err := task.Pause(ctx); err != nil {
				return err
			}
			defer func() {
				if err := task.Resume(ctx); err != nil {
					fmt.Println(errors.Wrap(err, "error resuming task"))
				}
			}()
		}

		if _, err := container.Checkpoint(ctx, ref, opts...); err != nil {
			return err
		}

		return nil
	},
}

var restoreCommand = cli.Command{
	Name:      "restore",
	Usage:     "restore a container from checkpoint",
	ArgsUsage: "CONTAINER REF",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "rw",
			Usage: "restore the rw layer from the checkpoint",
		},
		cli.BoolFlag{
			Name:  "live",
			Usage: "restore the runtime and memory data from the checkpoint",
		},
	},
	Action: func(context *cli.Context) error {
		id := context.Args().First()
		if id == "" {
			return errors.New("container id must be provided")
		}
		ref := context.Args().Get(1)
		if ref == "" {
			return errors.New("ref must be provided")
		}
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		checkpoint, err := client.GetImage(ctx, ref)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				return err
			}
			// TODO (ehazlett): consider other options (always/never fetch)
			ck, err := client.Fetch(ctx, ref)
			if err != nil {
				return err
			}
			checkpoint = containerd.NewImage(client, ck)
		}

		opts := []containerd.RestoreOpts{
			containerd.WithRestoreImage,
			containerd.WithRestoreSpec,
			containerd.WithRestoreRuntime,
		}
		if context.Bool("rw") {
			opts = append(opts, containerd.WithRestoreRW)
		}

		ctr, err := client.Restore(ctx, id, checkpoint, opts...)
		if err != nil {
			return err
		}

		topts := []containerd.NewTaskOpts{}
		if context.Bool("live") {
			topts = append(topts, containerd.WithTaskCheckpoint(checkpoint))
		}

		task, err := ctr.NewTask(ctx, cio.NewCreator(cio.WithStdio), topts...)
		if err != nil {
			return err
		}

		return task.Start(ctx)
	},
}
