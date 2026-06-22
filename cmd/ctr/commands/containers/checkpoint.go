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
	"fmt"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/errdefs"
	"github.com/urfave/cli/v2"
)

var checkpointCommand = &cli.Command{
	Name:      "checkpoint",
	Usage:     "Checkpoint a container",
	ArgsUsage: "CONTAINER REF",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "rw",
			Usage: "Include the rw layer in the checkpoint",
		},
		&cli.BoolFlag{
			Name:  "image",
			Usage: "Include the image in the checkpoint",
		},
		&cli.BoolFlag{
			Name:  "task",
			Usage: "Checkpoint container task",
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
		opts := []containerd.CheckpointOpts{
			containerd.WithCheckpointRuntime,
		}

		if cliContext.Bool("image") {
			opts = append(opts, containerd.WithCheckpointImage)
		}
		if cliContext.Bool("rw") {
			opts = append(opts, containerd.WithCheckpointRW)
		}
		if cliContext.Bool("task") {
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
					fmt.Println(fmt.Errorf("error resuming task: %w", err))
				}
			}()
		}

		if _, err := container.Checkpoint(ctx, ref, opts...); err != nil {
			return err
		}

		return nil
	},
}
