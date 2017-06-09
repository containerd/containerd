package main

import (
	"fmt"

	"github.com/containerd/containerd"
	"github.com/urfave/cli"
)

var deleteCommand = cli.Command{
	Name:      "delete",
	Usage:     "delete an existing container",
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
			return container.Delete(ctx)
		}
		status, err := task.Status(ctx)
		if err != nil {
			return err
		}
		if status == containerd.Stopped {
			if _, err := task.Delete(ctx); err != nil {
				return err
			}
			return container.Delete(ctx)
		}
		return fmt.Errorf("cannot delete a container with an existing task")
	},
}
