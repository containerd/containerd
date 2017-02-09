package main

import (
	gocontext "context"
	"errors"

	"github.com/davecgh/go-spew/spew"
	"github.com/docker/containerd/api/services/execution"
	"github.com/urfave/cli"
)

var inspectCommand = cli.Command{
	Name:      "inspect",
	Usage:     "inspect a container",
	ArgsUsage: "CONTAINER",
	Action: func(context *cli.Context) error {
		executionService, err := getExecutionService(context)
		if err != nil {
			return err
		}
		id := context.Args().First()
		if id == "" {
			return errors.New("container id must be provided")
		}
		getResponse, err := executionService.GetContainer(gocontext.Background(),
			&execution.GetContainerRequest{ID: id})
		if err != nil {
			return err
		}
		listProcResponse, err := executionService.ListProcesses(gocontext.Background(),
			&execution.ListProcessesRequest{ContainerID: id})
		if err != nil {
			return err
		}
		dumper := spew.NewDefaultConfig()
		dumper.Indent = "\t"
		dumper.DisableMethods = true
		dumper.DisablePointerAddresses = true
		dumper.Dump(getResponse, listProcResponse)
		return nil
	},
}
