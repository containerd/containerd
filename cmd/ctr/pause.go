package main

import (
	gocontext "context"
	"errors"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/urfave/cli"
)

var pauseCommand = cli.Command{
	Name:      "pause",
	Usage:     "pause an existing container",
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
		_, err = containers.Pause(gocontext.Background(), &execution.PauseRequest{
			ID: id,
		})
		return err
	},
}
