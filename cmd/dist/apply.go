package main

import (
	"os"

	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/log"
	"github.com/urfave/cli"
)

var applyCommand = cli.Command{
	Name:      "apply",
	Usage:     "apply layer from stdin to dir",
	ArgsUsage: "[flags] <path>",
	Flags:     []cli.Flag{},
	Action: func(context *cli.Context) error {
		var (
			dir = context.Args().First()
		)
		ctx, cancel := appContext()
		defer cancel()

		log.G(ctx).Info("applying layer from stdin")

		rd, err := compression.DecompressStream(os.Stdin)
		if err != nil {
			return err
		}

		if _, err := archive.Apply(ctx, dir, rd); err != nil {
			return err
		}

		return nil
	},
}
