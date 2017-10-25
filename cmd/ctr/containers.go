package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/urfave/cli"
)

var containersCommand = cli.Command{
	Name:    "containers",
	Usage:   "manage containers (metadata)",
	Aliases: []string{"c"},
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "quiet, q",
			Usage: "print only the container id",
		},
	},
	Subcommands: []cli.Command{
		containersDeleteCommand,
		containersSetLabelsCommand,
		containerInfoCommand,
	},
	ArgsUsage: "[filter, ...]",
	Action: func(context *cli.Context) error {
		var (
			filters = context.Args()
			quiet   = context.Bool("quiet")
		)
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		containers, err := client.Containers(ctx, filters...)
		if err != nil {
			return err
		}
		if quiet {
			for _, c := range containers {
				fmt.Printf("%s\n", c.ID())
			}
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 4, 8, 4, ' ', 0)
		fmt.Fprintln(w, "CONTAINER\tIMAGE\tRUNTIME\t")
		for _, c := range containers {
			info, err := c.Info(ctx)
			if err != nil {
				return err
			}
			imageName := info.Image
			if imageName == "" {
				imageName = "-"
			}
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t\n",
				c.ID(),
				imageName,
				info.Runtime.Name,
			); err != nil {
				return err
			}
		}
		return w.Flush()
	},
}
