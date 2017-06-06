package main

import (
	"errors"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/urfave/cli"
)

var pauseCommand = cli.Command{
	Name:      "pause",
	Usage:     "pause an existing container",
	ArgsUsage: "CONTAINER",
	Action: func(context *cli.Context) error {
		ctx, cancel := appContext(context)
		defer cancel()

		tasks, err := getTasksService(context)
		if err != nil {
			return err
		}
		id := context.Args().First()
		if id == "" {
			return errors.New("container id must be provided")
		}
		_, err = tasks.Pause(ctx, &execution.PauseRequest{
			ContainerID: id,
		})
		return err
	},
}
