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
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/cmd/ctr/commands/tasks"
	"github.com/containerd/containerd/errdefs"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

var restoreCommand = cli.Command{
	Name:      "restore",
	Usage:     "restore a container from checkpoint",
	ArgsUsage: "CONTAINER REF",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "rw",
			Usage: "Restore the rw layer from the checkpoint",
		},
		cli.BoolFlag{
			Name:  "live",
			Usage: "Restore the runtime and memory data from the checkpoint",
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
			logrus.WithError(err).Error("console resize")
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
			return cli.NewExitError("", int(code))
		}
		return nil
	},
}
