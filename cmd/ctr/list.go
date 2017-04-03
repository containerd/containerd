package main

import (
	gocontext "context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/urfave/cli"
)

var listCommand = cli.Command{
	Name:  "list",
	Usage: "list containers",
	Action: func(context *cli.Context) error {
		containers, err := getExecutionService(context)
		if err != nil {
			return err
		}
		response, err := containers.List(gocontext.Background(), &execution.ListRequest{})
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 10, 1, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tPID\tSTATUS")
		for _, c := range response.Containers {
			if _, err := fmt.Fprintf(w, "%s\t%d\t%s\n",
				c.ID,
				c.Pid,
				c.Status.String(),
			); err != nil {
				return err
			}
			if err := w.Flush(); err != nil {
				return err
			}
		}
		return nil
	},
}
