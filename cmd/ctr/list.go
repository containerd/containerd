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
		containers, err := getExecutionService(context)
		if err != nil {
			return err
		}
		response, err := containers.List(gocontext.Background(), &execution.ListRequest{})
		if err != nil {
			return err
		}

		if quiet {
			for _, c := range response.Containers {
				fmt.Println(c.ID)
			}
		} else {
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
		}

		return nil
	},
}
