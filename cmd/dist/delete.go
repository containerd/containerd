package main

import (
	contextpkg "context"
	"fmt"

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
	Flags: []cli.Flag{},
	Action: func(context *cli.Context) error {
		var (
			ctx       = contextpkg.Background()
			args      = []string(context.Args())
			exitError error
		)

		cs, err := resolveContentStore(context)
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
