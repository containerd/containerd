package main

import (
	gocontext "context"
	"errors"

	"github.com/docker/containerd/api/services/execution"
	"github.com/urfave/cli"
)

var deleteCommand = cli.Command{
	Name:      "delete",
	Usage:     "delete a process from containerd store",
	ArgsUsage: "CONTAINER",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "pid, p",
			Usage: "process id to be deleted",
		},
	},
	Action: func(context *cli.Context) error {
		executionService, err := getExecutionService(context)
		if err != nil {
			return err
		}

		id := context.Args().First()
		if id == "" {
			return errors.New("container id must be provided")
		}

		pid := uint32(context.Int64("pid"))
		if pid != 0 {
			_, err = executionService.DeleteProcess(gocontext.Background(), &execution.DeleteProcessRequest{
				ContainerID: id,
				Pid:         pid,
			})
			if err != nil {
				return err
			}
			return nil
		}

		if _, err := executionService.DeleteContainer(gocontext.Background(), &execution.DeleteContainerRequest{
			ID: id,
		}); err != nil {
			return err
		}

		return nil
	},
}
