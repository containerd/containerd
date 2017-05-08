package main

import (
	gocontext "context"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var deleteCommand = cli.Command{
	Name:      "delete",
	Usage:     "delete an existing container",
	ArgsUsage: "CONTAINER",
	Action: func(context *cli.Context) error {
		containers, err := getExecutionService(context)
		if err != nil {
			return err
		}
		id := context.Args().First()
		if id == "" {
			return errors.New("container id must be provided")
		}
		_, err = containers.Delete(gocontext.Background(), &execution.DeleteRequest{
			ID: id,
		})
		return err
	},
}
