package main

import (
	"fmt"

	"github.com/containerd/containerd"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var taskCheckpointCommand = cli.Command{
	Name:      "checkpoint",
	Usage:     "checkpoint a container",
	ArgsUsage: "CONTAINER",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "exit",
			Usage: "stop the container after the checkpoint",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			ctx, cancel = appContext(context)
			id          = context.Args().First()
		)
		defer cancel()

		if id == "" {
			return errors.New("container id must be provided")
		}
		client, err := newClient(context)
		if err != nil {
			return err
		}
		container, err := client.LoadContainer(ctx, id)
		if err != nil {
			return err
		}
		task, err := container.Task(ctx, nil)
		if err != nil {
			return err
		}
		var opts []containerd.CheckpointTaskOpts
		if context.Bool("exit") {
			opts = append(opts, containerd.WithExit)
		}
		checkpoint, err := task.Checkpoint(ctx, opts...)
		if err != nil {
			return err
		}
		fmt.Println(checkpoint.Digest.String())
		return nil
	},
}
