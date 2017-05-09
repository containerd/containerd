package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	units "github.com/docker/go-units"
	"github.com/urfave/cli"
)

var activeCommand = cli.Command{
	Name:        "active",
	Usage:       "display active transfers.",
	ArgsUsage:   "[flags] [<regexp>]",
	Description: `Display the ongoing transfers.`,
	Flags: []cli.Flag{
		cli.DurationFlag{
			Name:   "timeout, t",
			Usage:  "total timeout for fetch",
			EnvVar: "CONTAINERD_FETCH_TIMEOUT",
		},
		cli.StringFlag{
			Name:  "root",
			Usage: "path to content store root",
			Value: "/tmp/content", // TODO(stevvooe): for now, just use the PWD/.content
		},
	},
	Action: func(context *cli.Context) error {
		var (
			match = context.Args().First()
		)

		ctx, cancel := appContext()
		defer cancel()

		cs, err := resolveContentStore(context)
		if err != nil {
			return err
		}

		active, err := cs.Status(ctx, match)
		if err != nil {
			return err
		}

		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
		fmt.Fprintln(tw, "REF\tSIZE\tAGE")
		for _, active := range active {
			fmt.Fprintf(tw, "%s\t%s\t%s\n",
				active.Ref,
				units.HumanSize(float64(active.Offset)),
				units.HumanDuration(time.Since(active.StartedAt)))
		}
		tw.Flush()

		return nil
	},
}
