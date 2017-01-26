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

var deleteCommand = cli.Command{
	Name:      "delete",
	Aliases:   []string{"del"},
	Usage:     "permanently delete one or more blobs.",
	ArgsUsage: "[flags] [<digest>, ...]",
	Description: `Delete one or more blobs permanently. Successfully deleted
	blobs are printed to stdout.`,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "root",
			Usage: "path to content store root",
			Value: ".content", // TODO(stevvooe): for now, just use the PWD/.content
		},
	},
	Action: func(context *cli.Context) error {
		var (
			ctx       = contextpkg.Background()
			root      = context.String("root")
			args      = []string(context.Args())
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

		for _, arg := range args {
			dgst, err := digest.Parse(arg)
			if err != nil {
				if exitError == nil {
					exitError = err
				}
				log.G(ctx).WithError(err).Errorf("could not delete %v", dgst)
				continue
			}

			if err := cs.Delete(dgst); err != nil {
				if exitError == nil {
					exitError = err
				}
				log.G(ctx).WithError(err).Errorf("could not delete %v", dgst)
				continue
			}

			fmt.Println(dgst)
		}

		return exitError
	},
}
