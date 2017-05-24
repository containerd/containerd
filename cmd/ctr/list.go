package main

import (
	gocontext "context"
	"fmt"
	"os"
	"text/tabwriter"

	containersapi "github.com/containerd/containerd/api/services/containers"
	"github.com/containerd/containerd/api/services/execution"
	tasktypes "github.com/containerd/containerd/api/types/task"
	"github.com/urfave/cli"
)

var containersCommand = cli.Command{
	Name: "containers",
}

var listCommand = cli.Command{
	Name:    "list",
	Aliases: []string{"ls"},
	Usage:   "list containers",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "quiet, q",
			Usage: "print only the container id",
		},
	},
	Action: func(context *cli.Context) error {
		quiet := context.Bool("quiet")

		tasks, err := getTasksService(context)
		if err != nil {
			return err
		}

		containers, err := getContainersService(context)
		if err != nil {
			return err
		}

		response, err := containers.List(gocontext.Background(), &containersapi.ListContainersRequest{})
		if err != nil {
			return err
		}

		if quiet {
			for _, c := range response.Containers {
				fmt.Println(c.ID)
			}
		} else {

			tasksResponse, err := tasks.List(gocontext.TODO(), &execution.ListRequest{})
			if err != nil {
				return err
			}

			// Join with tasks to get status.
			tasksByContainerID := map[string]*tasktypes.Task{}
			for _, task := range tasksResponse.Tasks {
				task.Descriptor()
				tasksByContainerID[task.ContainerID] = task
			}

			w := tabwriter.NewWriter(os.Stdout, 10, 1, 3, ' ', 0)
			fmt.Fprintln(w, "ID\tIMAGE\tPID\tSTATUS")
			for _, c := range response.Containers {
				var (
					status string
					pid    uint32
				)
				task, ok := tasksByContainerID[c.ID]
				if ok {
					status = task.Status.String()
					pid = task.Pid
				} else {
					status = "STOPPED" // TODO(stevvooe): Is this assumption correct?
					pid = 0
				}

				if _, err := fmt.Fprintf(w, "%s\t%s\t%d\t%s\n",
					c.ID,
					c.Image,
					pid,
					status,
				); err != nil {
					return err
				}
				if err := w.Flush(); err != nil {
					return err
				}
			}
		}

		return nil
	},
}
