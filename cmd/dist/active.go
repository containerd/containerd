package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/docker/containerd/content"
	units "github.com/docker/go-units"
	"github.com/urfave/cli"
)

var activeCommand = cli.Command{
	Name:        "active",
	Usage:       "display active transfers.",
	ArgsUsage:   "[flags] [<key>, ...]",
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
			Value: ".content", // TODO(stevvooe): for now, just use the PWD/.content
		},
	},
	Action: func(context *cli.Context) error {
		var (
			// ctx  = contextpkg.Background()
			root = context.String("root")
		)

		if !filepath.IsAbs(root) {
			var err error
			root, err = filepath.Abs(root)
			if err != nil {
				return err
			}
		}

		cs, err := content.Open(root)
		if err != nil {
			return err
		}

		active, err := cs.Active()
		if err != nil {
			return err
		}

		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
		fmt.Fprintln(tw, "REF\tSIZE\tAGE")
		for _, active := range active {
			fmt.Fprintf(tw, "%s\t%s\t%s\n",
				active.Ref,
				units.HumanSize(float64(active.Size)),
				units.HumanDuration(time.Since(active.ModTime)))
		}
		tw.Flush()

		return nil
	},
}
