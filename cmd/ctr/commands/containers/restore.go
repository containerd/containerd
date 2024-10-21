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
	"errors"

	"github.com/containerd/console"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/tasks"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/urfave/cli/v2"
)

var restoreCommand = &cli.Command{
	Name:      "restore",
	Usage:     "Restore a container from checkpoint",
	ArgsUsage: "CONTAINER REF",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "rw",
			Usage: "Restore the rw layer from the checkpoint",
		},
		&cli.BoolFlag{
			Name:  "live",
			Usage: "Restore the runtime and memory data from the checkpoint",
		},
		&cli.StringFlag{
			Name:  "image-path",
			Usage: "Path to criu image files",
		},
	},
	Action: func(cliContext *cli.Context) error {
		id := cliContext.Args().First()
		if id == "" {
			return errors.New("container id must be provided")
		}
		ref := cliContext.Args().Get(1)
		if ref == "" {
			return errors.New("ref must be provided")
		}
		client, ctx, cancel, err := commands.NewClient(cliContext)
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
		if cliContext.Bool("rw") {
			opts = append(opts, containerd.WithRestoreRW)
		}

		ctr, err := client.Restore(ctx, id, checkpoint, opts...)
		if err != nil {
			return err
		}
		topts := []containerd.NewTaskOpts{}
		imagePath := cliContext.String("image-path")
		switch cliContext.Bool("live") {
		case true:
			if imagePath == "" {
				topts = append(topts, containerd.WithTaskCheckpoint(checkpoint))
			} else {
				return errors.New("live and image-path must not be both provided")
			}
		case false:
			if imagePath != "" {
				topts = append(topts, containerd.WithRestoreImagePath(imagePath))
			}
		}
		spec, err := ctr.Spec(ctx)
		if err != nil {
			return err
		}

		useTTY := spec.Process.Terminal

		var con console.Console
		if useTTY {
			con = console.Current()
			defer con.Reset()
			if err := con.SetRaw(); err != nil {
				return err
			}
		}

		task, err := tasks.NewTask(ctx, client, ctr, "", con, false, "", []cio.Opt{}, topts...)
		if err != nil {
			return err
		}

		var statusC <-chan containerd.ExitStatus
		if useTTY {
			if statusC, err = task.Wait(ctx); err != nil {
				return err
			}
		}

		if err := task.Start(ctx); err != nil {
			return err
		}
		if !useTTY {
			return nil
		}

		if err := tasks.HandleConsoleResize(ctx, task, con); err != nil {
			log.G(ctx).WithError(err).Error("console resize")
		}

		status := <-statusC
		code, _, err := status.Result()
		if err != nil {
			return err
		}
		if _, err := task.Delete(ctx); err != nil {
			return err
		}
		if code != 0 {
			return cli.Exit("", int(code))
		}
		return nil
	},
}
