package main

import (
	gocontext "context"
	"fmt"

	"github.com/docker/containerd/api/execution"
	"github.com/urfave/cli"
)

var listCommand = cli.Command{
	Name:  "list",
	Usage: "list containers",
	Action: func(context *cli.Context) error {
		executionService, err := getExecutionService(context)
		if err != nil {
			return err
		}
		listResponse, err := executionService.ListContainers(gocontext.Background(), &execution.ListContainersRequest{
			Owner: []string{},
		})
		if err != nil {
			return err
		}
		fmt.Println("ID\tSTATUS\tPROCS\tBUNDLE")
		for _, c := range listResponse.Containers {
			listProcResponse, err := executionService.ListProcesses(gocontext.Background(),
				&execution.ListProcessesRequest{ContainerID: c.ID})
			if err != nil {
				return err
			}
			fmt.Printf("%s\t%s\t%d\t%s\n",
				c.ID,
				c.Status,
				len(listProcResponse.Processes),
				c.Bundle,
			)
		}
		return nil
	},
}
