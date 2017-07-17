package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	tasks "github.com/containerd/containerd/api/services/tasks/v1"
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

var containersListCommand = cli.Command{
	Name:      "list",
	Usage:     "list all tasks or those that match a filter",
	ArgsUsage: "[filter, ...]",
	Aliases:   []string{"ls"},
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "quiet, q",
			Usage: "print only the container id",
		},
	},
	Action: containerListFn,
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

	s := client.TaskService()
	response, err := s.List(ctx, &tasks.ListTasksRequest{})
	if err != nil {
		return err
	}

	if quiet {
		for _, task := range response.Tasks {
			fmt.Println(task.ID)
		}
	} else {
		w := tabwriter.NewWriter(os.Stdout, 4, 8, 4, ' ', 0)
		fmt.Fprintln(w, "TASK\tPID\tSTATUS\t")
		for _, task := range response.Tasks {
			if _, err := fmt.Fprintf(w, "%s\t%d\t%s\n",
				task.ID,
				task.Pid,
				task.Status.String(),
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
		filters     = context.Args()
		quiet       = context.Bool("quiet")
		ctx, cancel = appContext(context)
	)
	defer cancel()

	client, err := newClient(context)
	if err != nil {
		return err
	}
	containers, err := client.Containers(ctx, filters...)
	if err != nil {
		return err
	}

	if quiet {
		for _, c := range containers {
			fmt.Printf("%s\n", c.ID())
		}
	} else {
		w := tabwriter.NewWriter(os.Stdout, 4, 8, 4, ' ', 0)
		fmt.Fprintln(w, "CONTAINER\tIMAGE\tRUNTIME\tLABELS\t")
		for _, c := range containers {
			var labelStrings []string
			for k, v := range c.Info().Labels {
				labelStrings = append(labelStrings, strings.Join([]string{k, v}, "="))
			}
			labels := strings.Join(labelStrings, ",")
			if labels == "" {
				labels = "-"
			}

			imageName := c.Info().Image
			if imageName == "" {
				imageName = "-"
			}

			record := c.Info()
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%v\t\n",
				c.ID(),
				imageName,
				record.Runtime.Name,
				labels,
			); err != nil {
				return err
			}
		}
		return w.Flush()
	}
	return nil
}
