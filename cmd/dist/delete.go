package main

import (
	"fmt"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	digest "github.com/opencontainers/go-digest"
	"github.com/urfave/cli"
)

var deleteCommand = cli.Command{
	Name:      "delete",
	Aliases:   []string{"del", "remove", "rm"},
	Usage:     "permanently delete one or more blobs.",
	ArgsUsage: "[flags] [<digest>, ...]",
	Description: `Delete one or more blobs permanently. Successfully deleted
	blobs are printed to stdout.`,
	Flags: []cli.Flag{},
	Action: func(context *cli.Context) error {
		var (
			args      = []string(context.Args())
			exitError error
		)
		ctx, cancel := appContext(context)
		defer cancel()

		client, err := getClient(context)
		if err != nil {
			return err
		}

		cs := client.ContentStore()

		for _, arg := range args {
			dgst, err := digest.Parse(arg)
			if err != nil {
				if exitError == nil {
					exitError = err
				}
				log.G(ctx).WithError(err).Errorf("could not delete %v", dgst)
				continue
			}

			if err := cs.Delete(ctx, dgst); err != nil {
				if !errdefs.IsNotFound(err) {
					if exitError == nil {
						exitError = err
					}
					log.G(ctx).WithError(err).Errorf("could not delete %v", dgst)
				}
				continue
			}

			fmt.Println(dgst)
		}

		return exitError
	},
}
