package main

import (
	"errors"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/urfave/cli"
)

var resumeCommand = cli.Command{
	Name:      "resume",
	Usage:     "resume a paused container",
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
		_, err = tasks.Resume(ctx, &execution.ResumeRequest{
			ContainerID: id,
		})
		return err
	},
}
