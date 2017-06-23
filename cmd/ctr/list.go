package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/containerd/containerd"
	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	tasktypes "github.com/containerd/containerd/api/types/task"
	"github.com/urfave/cli"
)

var taskListCommand = cli.Command{
	Name:    "tasks",
	Usage:   "manage tasks",
	Aliases: []string{"t"},
	Subcommands: []cli.Command{
		{
			Name:    "list",
			Usage:   "list tasks",
			Aliases: []string{"ls"},
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:  "quiet, q",
					Usage: "print only the task id & pid",
				},
			},
			Action: taskListFn,
		},
	},
}

var containerListCommand = cli.Command{
	Name:    "containers",
	Usage:   "manage containers (metadata)",
	Aliases: []string{"c"},
	Subcommands: []cli.Command{
		{
			Name:    "list",
			Usage:   "list tasks",
			Aliases: []string{"ls"},
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:  "quiet, q",
					Usage: "print only the container id",
				},
			},
			Action: containerListFn,
		},
	},
}

func taskListFn(context *cli.Context) error {
	var (
		quiet       = context.Bool("quiet")
		ctx, cancel = appContext(context)
	)
	defer cancel()

	client, err := newClient(context)
	if err != nil {
		return err
	}
	containers, err := client.Containers(ctx)
	if err != nil {
		return err
	}

	s := client.TaskService()

	tasksResponse, err := s.List(ctx, &tasks.ListTasksRequest{})
	if err != nil {
		return err
	}

	// Join with tasks to get status.
	tasksByContainerID := map[string]*tasktypes.Task{}
	for _, task := range tasksResponse.Tasks {
		tasksByContainerID[task.ContainerID] = task
	}

	if quiet {
		for _, c := range containers {
			task, ok := tasksByContainerID[c.ID()]
			if ok {
				fmt.Printf("%s\t%d\n", c.ID(), task.Pid)
			} else {
				//Since task is not running, PID is printed 0
				fmt.Printf("%s\t%d\n", c.ID(), 0)
			}
		}
	} else {

		w := tabwriter.NewWriter(os.Stdout, 10, 1, 3, ' ', 0)
		fmt.Fprintln(w, "TASK-ID\tIMAGE\tPID\tSTATUS")
		for _, c := range containers {
			var imageName string
			if image, err := c.Image(ctx); err != nil {
				if err != containerd.ErrNoImage {
					return err
				}
				imageName = "-"
			} else {
				imageName = image.Name()
			}
			var (
				status string
				pid    uint32
			)
			task, ok := tasksByContainerID[c.ID()]
			if ok {
				status = task.Status.String()
				pid = task.Pid
			} else {
				status = "STOPPED" // TODO(stevvooe): Is this assumption correct?
				pid = 0
			}

			if _, err := fmt.Fprintf(w, "%s\t%s\t%d\t%s\n",
				c.ID(),
				imageName,
				pid,
				status,
			); err != nil {
				return err
			}
		}
		return w.Flush()
	}
	return nil
}

func containerListFn(context *cli.Context) error {
	var (
		quiet       = context.Bool("quiet")
		ctx, cancel = appContext(context)
	)
	defer cancel()

	client, err := newClient(context)
	if err != nil {
		return err
	}
	containers, err := client.Containers(ctx)
	if err != nil {
		return err
	}
	if quiet {
		for _, c := range containers {
			fmt.Printf("%s\n", c.ID())
		}
	} else {
		cl := tabwriter.NewWriter(os.Stdout, 10, 1, 3, ' ', 0)
		fmt.Fprintln(cl, "ID\tIMAGE\tRUNTIME\tSIZE")
		for _, c := range containers {
			var imageName string
			if image, err := c.Image(ctx); err != nil {
				if err != containerd.ErrNoImage {
					return err
				}
				imageName = "-"
			} else {
				imageName = image.Name()
			}
			proto := c.Proto()
			if _, err := fmt.Fprintf(cl, "%s\t%s\t%s\t%d\n",
				c.ID(),
				imageName,
				proto.Runtime,
				proto.Size(),
			); err != nil {
				return err
			}
		}
		return cl.Flush()
	}
	return nil
}
