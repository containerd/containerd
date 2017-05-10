package main

import (
	"io"
	"os"

	"github.com/containerd/containerd/log"
	"github.com/urfave/cli"
)

// TODO(stevvooe): Create "multi-fetch" mode that just takes a remote
// then receives object/hint lines on stdin, returning content as
// needed.

var fetchObjectCommand = cli.Command{
	Name:        "fetch-object",
	Usage:       "retrieve objects from a remote",
	ArgsUsage:   "[flags] <remote> <object> [<hint>, ...]",
	Description: `Fetch objects by identifier from a remote.`,
	Flags:       registryFlags,
	Action: func(context *cli.Context) error {
		var (
			ref = context.Args().First()
		)
		ctx, cancel := appContext()
		defer cancel()

		resolver, err := getResolver(ctx, context)
		if err != nil {
			return err
		}

		ctx = log.WithLogger(ctx, log.G(ctx).WithField("ref", ref))

		log.G(ctx).Infof("resolving")
		_, desc, fetcher, err := resolver.Resolve(ctx, ref)
		if err != nil {
			return err
		}

		log.G(ctx).Infof("fetching")
		rc, err := fetcher.Fetch(ctx, desc)
		if err != nil {
			return err
		}
		defer rc.Close()

		_, err = io.Copy(os.Stdout, rc)
		return err
	},
}
