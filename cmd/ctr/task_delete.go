package main

import "github.com/urfave/cli"

var taskDeleteCommand = cli.Command{
	Name:      "delete",
	Usage:     "delete a task",
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
		status, err := task.Delete(ctx)
		if err != nil {
			return err
		}
		if ec := status.ExitCode(); ec != 0 {
			return cli.NewExitError("", int(ec))
		}
		return nil
	},
}
