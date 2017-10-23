package main

import "github.com/urfave/cli"

var taskPauseCommand = cli.Command{
	Name:      "pause",
	Usage:     "pause an existing container",
	ArgsUsage: "CONTAINER",
	Action: func(context *cli.Context) error {
		client, ctx, cancel, err := newClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		container, err := client.LoadContainer(ctx, context.Args().First())
		if err != nil {
			return err
		}
		task, err := container.Task(ctx, nil)
		if err != nil {
			return err
		}
		return task.Pause(ctx)
	},
}
