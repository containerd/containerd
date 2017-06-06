package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var psCommand = cli.Command{
	Name:  "ps",
	Usage: "list processes for container",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "id",
			Usage: "id of the container",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			id          = context.String("id")
			ctx, cancel = appContext(context)
		)
		defer cancel()

		if id == "" {
			return errors.New("container id must be provided")
		}

		pr := &execution.ProcessesRequest{
			ContainerID: id,
		}

		tasks, err := getTasksService(context)
		if err != nil {
			return err
		}

		resp, err := tasks.Processes(ctx, pr)
		if err != nil {
			return err
		}

		w := tabwriter.NewWriter(os.Stdout, 10, 1, 3, ' ', 0)
		fmt.Fprintln(w, "PID")
		for _, ps := range resp.Processes {
			if _, err := fmt.Fprintf(w, "%d\n",
				ps.Pid,
			); err != nil {
				return err
			}
		}
		if err := w.Flush(); err != nil {
			return err
		}

		return nil
	},
}
