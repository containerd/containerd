package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/log"
	units "github.com/docker/go-units"
	digest "github.com/opencontainers/go-digest"
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
			walkFn = func(path string, fi os.FileInfo, dgst digest.Digest) error {
				fmt.Println(dgst)
				return nil
			}
		} else {
			tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
			defer tw.Flush()

			fmt.Fprintln(tw, "DIGEST\tSIZE\tAGE")
			walkFn = func(path string, fi os.FileInfo, dgst digest.Digest) error {
				fmt.Fprintf(tw, "%s\t%s\t%s\n",
					dgst,
					units.HumanSize(float64(fi.Size())),
					units.HumanDuration(time.Since(fi.ModTime())))
				return nil
			}

		}

		return cs.Walk(walkFn)
	},
}
