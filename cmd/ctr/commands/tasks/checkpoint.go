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

package tasks

import (
	"errors"
	"fmt"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/runtime/v2/runc/options"
	"github.com/urfave/cli"
)

var checkpointCommand = cli.Command{
	Name:      "checkpoint",
	Usage:     "Checkpoint a container",
	ArgsUsage: "[flags] CONTAINER",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "exit",
			Usage: "Stop the container after the checkpoint",
		},
		cli.StringFlag{
			Name:  "image-path",
			Usage: "Path to criu image files",
		},
		cli.StringFlag{
			Name:  "work-path",
			Usage: "Path to criu work files and logs",
		},
	},
	Action: func(context *cli.Context) error {
		id := context.Args().First()
		if id == "" {
			return errors.New("container id must be provided")
		}
		client, ctx, cancel, err := commands.NewClient(context, containerd.WithDefaultRuntime(context.String("runtime")))
		if err != nil {
			return err
		}
		defer cancel()
		container, err := client.LoadContainer(ctx, id)
		if err != nil {
			return err
		}
		task, err := container.Task(ctx, nil)
		if err != nil {
			return err
		}
		info, err := container.Info(ctx)
		if err != nil {
			return err
		}
		opts := []containerd.CheckpointTaskOpts{withCheckpointOpts(info.Runtime.Name, context)}
		checkpoint, err := task.Checkpoint(ctx, opts...)
		if err != nil {
			return err
		}
		if context.String("image-path") == "" {
			fmt.Println(checkpoint.Name())
		}
		return nil
	},
}

// withCheckpointOpts only suitable for runc runtime now
func withCheckpointOpts(rt string, context *cli.Context) containerd.CheckpointTaskOpts {
	return func(r *containerd.CheckpointTaskInfo) error {
		imagePath := context.String("image-path")
		workPath := context.String("work-path")

		if r.Options == nil {
			r.Options = &options.CheckpointOptions{}
		}
		opts, _ := r.Options.(*options.CheckpointOptions)

		if context.Bool("exit") {
			opts.Exit = true
		}
		if imagePath != "" {
			opts.ImagePath = imagePath
		}
		if workPath != "" {
			opts.WorkPath = workPath
		}

		return nil
	}
}
