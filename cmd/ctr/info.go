package main

import (
	"encoding/json"

	gocontext "context"
	"fmt"

	containersapi "github.com/containerd/containerd/api/services/containers"
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

		containers, err := getContainersService(context)
		if err != nil {
			return err
		}
		tasks, err := getTasksService(context)
		if err != nil {
			return err
		}

		containerResponse, err := containers.Get(gocontext.TODO(), &containersapi.GetContainerRequest{ID: id})
		if err != nil {
			return err
		}

		// TODO(stevvooe): Just dumping the container and the task, for now. We
		// should split this into two separate commands.
		cjson, err := json.MarshalIndent(containerResponse, "", "    ")
		if err != nil {
			return err
		}

		fmt.Println(string(cjson))

		response, err := tasks.Info(gocontext.Background(), &execution.InfoRequest{ContainerID: id})
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
