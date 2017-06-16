package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/containerd/containerd"
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
				fmt.Println(c.ID())
			}
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 10, 1, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tIMAGE\tPID\tSTATUS")
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
			task, err := c.Task(ctx, nil)
			if err == nil {
				s, err := task.Status(ctx)
				if err != nil {
					return err
				}
				status = string(s)
				pid = task.Pid()
			} else {
				if err != containerd.ErrNoRunningTask {
					return err
				}
				status = string(containerd.Stopped)
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
	},
}
