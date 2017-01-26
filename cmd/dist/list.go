package main

import (
	contextpkg "context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/docker/containerd/content"
	"github.com/docker/containerd/log"
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
		cli.StringFlag{
			Name:  "root",
			Usage: "path to content store root",
			Value: ".content", // TODO(stevvooe): for now, just use the PWD/.content
		},
		cli.BoolFlag{
			Name:  "quiet, q",
			Usage: "print only the blob digest",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			ctx   = contextpkg.Background()
			root  = context.String("root")
			quiet = context.Bool("quiet")
			args  = []string(context.Args())
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

			fmt.Fprintf(tw, "DIGEST\tSIZE\tAGE\n")
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
