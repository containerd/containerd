package main

import (
	"fmt"

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
		if _, err := container.Task(ctx, nil); err == nil {
			return fmt.Errorf("cannot delete a container with a running task")
		}
		return container.Delete(ctx)
	},
}
