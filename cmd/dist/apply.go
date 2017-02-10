package main

import (
	contextpkg "context"
	"os"

	"github.com/docker/containerd/archive"
	"github.com/docker/containerd/log"
	dockerarchive "github.com/docker/docker/pkg/archive"
	"github.com/urfave/cli"
)

var applyCommand = cli.Command{
	Name:      "apply",
	Usage:     "apply layer from stdin to dir",
	ArgsUsage: "[flags] <digest>",
	Flags:     []cli.Flag{},
	Action: func(context *cli.Context) error {
		var (
			ctx = contextpkg.Background()
			dir = context.Args().First()
		)

		log.G(ctx).Info("applying layer from stdin")

		rd, err := dockerarchive.DecompressStream(os.Stdin)
		if err != nil {
			return err
		}

		if _, err := archive.Apply(ctx, dir, rd); err != nil {
			return err
		}

		return nil
	},
}
