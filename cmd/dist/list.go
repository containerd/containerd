package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/containerd/containerd/content"
	units "github.com/docker/go-units"
	"github.com/urfave/cli"
)

var listCommand = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "list all blobs in the store.",
	ArgsUsage:   "[flags] [<filter>, ...]",
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
		ctx, cancel := appContext(context)
		defer cancel()

		cs, err := resolveContentStore(context)
		if err != nil {
			return err
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

			fmt.Fprintln(tw, "DIGEST\tSIZE\tAGE\tLABELS")
			walkFn = func(info content.Info) error {
				var labelStrings []string
				for k, v := range info.Labels {
					labelStrings = append(labelStrings, strings.Join([]string{k, v}, "="))
				}
				labels := strings.Join(labelStrings, ",")
				if labels == "" {
					labels = "-"
				}

				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
					info.Digest,
					units.HumanSize(float64(info.Size)),
					units.HumanDuration(time.Since(info.CommittedAt)),
					labels)
				return nil
			}

		}

		return cs.Walk(ctx, walkFn, args...)
	},
}
