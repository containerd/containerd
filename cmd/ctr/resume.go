package main

import "github.com/urfave/cli"

var taskResumeCommand = cli.Command{
	Name:      "resume",
	Usage:     "resume a paused container",
	ArgsUsage: "CONTAINER",
	Action: func(context *cli.Context) error {
		ctx, cancel := appContext(context)
		defer cancel()

		client, err := newClient(context)
		if err != nil {
			return err
		}
		container, err := client.LoadContainer(ctx, context.Args().First())
		if err != nil {
			return err
		}
		task, err := container.Task(ctx, nil)
		if err != nil {
			return err
		}
		return task.Resume(ctx)
	},
}
