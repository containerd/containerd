package main

import (
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var containerInfoCommand = cli.Command{
	Name:      "info",
	Usage:     "get info about a container",
	ArgsUsage: "CONTAINER",
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

		printAsJSON(container.Info())

		return nil
	},
}
