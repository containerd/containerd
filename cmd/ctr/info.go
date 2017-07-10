package main

import (
	"encoding/json"

	"fmt"

	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var infoCommand = cli.Command{
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
		cjson, err := json.MarshalIndent(container.Proto(), "", "    ")
		if err != nil {
			return err
		}
		fmt.Println(string(cjson))
		return nil
	},
}
