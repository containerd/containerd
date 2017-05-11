package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/log"
	units "github.com/docker/go-units"
	"github.com/urfave/cli"
)

var listCommand = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "list all blobs in the store.",
	ArgsUsage:   "[flags] [<prefix>, ...]",
	Description: `List blobs in the content store.`,
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "quiet, q",
			Usage: "print only the blob digest",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			quiet = context.Bool("quiet")
			args  = []string(context.Args())
		)
		ctx, cancel := appContext()
		defer cancel()

		cs, err := resolveContentStore(context)
		if err != nil {
			return err
		}

		if len(args) > 0 {
			// TODO(stevvooe): Implement selection of a few blobs. Not sure
			// what kind of efficiency gains we can actually get here.
			log.G(ctx).Warnf("args ignored; need to implement matchers")
		}

		var walkFn content.WalkFunc
		if quiet {
			walkFn = func(info content.Info) error {
				fmt.Println(info.Digest)
				return nil
			}
		} else {
			tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
			defer tw.Flush()

			fmt.Fprintln(tw, "DIGEST\tSIZE\tAGE")
			walkFn = func(info content.Info) error {
				fmt.Fprintf(tw, "%s\t%s\t%s\n",
					info.Digest,
					units.HumanSize(float64(info.Size)),
					units.HumanDuration(time.Since(info.CommittedAt)))
				return nil
			}

		}

		return cs.Walk(ctx, walkFn)
	},
}
