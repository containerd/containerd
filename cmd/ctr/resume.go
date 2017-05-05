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
		containers, err := getExecutionService(context)
		if err != nil {
			return err
		}
		id := context.Args().First()
		if id == "" {
			return errors.New("container id must be provided")
		}
		_, err = containers.Resume(gocontext.Background(), &execution.ResumeRequest{
			ID: id,
		})
		return err
	},
}
