package main

import (
	gocontext "context"
	"errors"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/urfave/cli"
)

var resumeCommand = cli.Command{
	Name:      "resume",
	Usage:     "resume a paused container",
	ArgsUsage: "CONTAINER",
	Action: func(context *cli.Context) error {
		tasks, err := getTasksService(context)
		if err != nil {
			return err
		}
		id := context.Args().First()
		if id == "" {
			return errors.New("container id must be provided")
		}
		_, err = tasks.Resume(gocontext.Background(), &execution.ResumeRequest{
			ContainerID: id,
		})
		return err
	},
}
