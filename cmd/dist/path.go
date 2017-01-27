package main

import (
	contextpkg "context"
	"fmt"
	"path/filepath"

	"github.com/docker/containerd/content"
	"github.com/docker/containerd/log"
	digest "github.com/opencontainers/go-digest"
	"github.com/urfave/cli"
)

var pathCommand = cli.Command{
	Name:      "path",
	Usage:     "print the path to one or more blobs",
	ArgsUsage: "[flags] [<digest>, ...]",
	Description: `Display the paths to one or more blobs.
	
Output paths can be used to directly access blobs on disk.`,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:   "root",
			Usage:  "path to content store root",
			Value:  ".content", // TODO(stevvooe): for now, just use the PWD/.content
			EnvVar: "CONTAINERD_DIST_CONTENT_STORE",
		},
		cli.BoolFlag{
			Name:  "quiet, q",
			Usage: "elide digests in output",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			ctx       = contextpkg.Background()
			root      = context.String("root")
			args      = []string(context.Args())
			quiet     = context.Bool("quiet")
			exitError error
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

		// TODO(stevvooe): Take the set of paths from stdin.

		if len(args) < 1 {
			return fmt.Errorf("please specify a blob digest")
		}

		for _, arg := range args {
			dgst, err := digest.Parse(arg)
			if err != nil {
				log.G(ctx).WithError(err).Errorf("parsing %q as digest failed", arg)
				if exitError == nil {
					exitError = err
				}
				continue
			}

			p, err := cs.GetPath(dgst)
			if err != nil {
				log.G(ctx).WithError(err).Errorf("getting path for %q failed", dgst)
				if exitError == nil {
					exitError = err
				}
				continue
			}

			if !quiet {
				fmt.Println(dgst, p)
			} else {
				fmt.Println(p)
			}
		}

		return exitError
	},
}
