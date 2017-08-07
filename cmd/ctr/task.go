package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/urfave/cli"
)

var tasksCommand = cli.Command{
	Name:    "tasks",
	Usage:   "manage tasks",
	Aliases: []string{"t"},
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "quiet, q",
			Usage: "print only the task id & pid",
		},
	},
	Subcommands: []cli.Command{
		taskAttachCommand,
		taskCheckpointCommand,
		taskExecCommand,
		taskKillCommand,
		taskPauseCommand,
		taskPsCommand,
		taskResumeCommand,
		taskStartCommand,
		taskDeleteCommand,
	},
	Action: func(context *cli.Context) error {
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
			return nil
		}
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
	},
}
