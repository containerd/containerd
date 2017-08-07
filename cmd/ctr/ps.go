package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var taskPsCommand = cli.Command{
	Name:      "ps",
	Usage:     "list processes for container",
	ArgsUsage: "CONTAINER",
	Action: func(context *cli.Context) error {
		var (
			id          = context.Args().First()
			ctx, cancel = appContext(context)
		)
		defer cancel()

		if id == "" {
			return errors.New("container id must be provided")
		}
		client, err := newClient(context)
		if err != nil {
			return err
		}
		container, err := client.LoadContainer(ctx, id)
		if err != nil {
			return err
		}

		task, err := container.Task(ctx, nil)
		if err != nil {
			return err
		}
		processes, err := task.Pids(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 10, 1, 3, ' ', 0)
		fmt.Fprintln(w, "PID")
		for _, ps := range processes {
			if _, err := fmt.Fprintf(w, "%d\n", ps); err != nil {
				return err
			}
		}
		if err := w.Flush(); err != nil {
			return err
		}
		return nil
	},
}
