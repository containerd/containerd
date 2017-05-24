package main

import (
	gocontext "context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/urfave/cli"
)

var eventsCommand = cli.Command{
	Name:  "events",
	Usage: "display containerd events",
	Action: func(context *cli.Context) error {
		tasks, err := getTasksService(context)
		if err != nil {
			return err
		}
		events, err := tasks.Events(gocontext.Background(), &execution.EventsRequest{})
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 10, 1, 3, ' ', 0)
		fmt.Fprintln(w, "TYPE\tID\tPID\tEXIT_STATUS")
		for {
			e, err := events.Recv()
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w,
				"%s\t%s\t%d\t%d\n",
				e.Type.String(),
				e.ID,
				e.Pid,
				e.ExitStatus,
			); err != nil {
				return err
			}
			if err := w.Flush(); err != nil {
				return err
			}
		}
	},
}
