package main

import (
	"encoding/json"

	gocontext "context"
	"fmt"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var infoCommand = cli.Command{
	Name:  "info",
	Usage: "get info about a container",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "id",
			Usage: "id of the container",
		},
	},
	Action: func(context *cli.Context) error {
		id := context.String("id")
		if id == "" {
			return errors.New("container id must be provided")
		}

		containers, err := getExecutionService(context)
		if err != nil {
			return err
		}
		response, err := containers.Info(gocontext.Background(), &execution.InfoRequest{ID: id})
		if err != nil {
			return err
		}
		json, err := json.MarshalIndent(response, "", "    ")
		if err != nil {
			return err
		}
		fmt.Println(string(json))
		return nil
	},
}
