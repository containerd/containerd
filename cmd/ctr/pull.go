package main

import (
	"fmt"

	"github.com/containerd/containerd/log"
	"github.com/urfave/cli"
)

var pullCommand = cli.Command{
	Name:      "pull",
	Usage:     "pull an image from a remote",
	ArgsUsage: "[flags] <ref>",
	Description: `Fetch and prepare an image for use in containerd.

After pulling an image, it should be ready to use the same reference in a run
command. As part of this process, we do the following:

1. Fetch all resources into containerd.
2. Prepare the snapshot filesystem with the pulled resources.
3. Register metadata for the image.
`,
	Flags: append(registryFlags, snapshotterFlags...),
	Action: func(clicontext *cli.Context) error {
		var (
			ref = clicontext.Args().First()
		)

		ctx, cancel := appContext(clicontext)
		defer cancel()

		img, err := fetch(ctx, ref, clicontext)
		if err != nil {
			return err
		}

		log.G(ctx).WithField("image", ref).Debug("unpacking")

		// TODO: Show unpack status
		fmt.Printf("unpacking %s...\n", img.Target().Digest)
		err = img.Unpack(ctx, clicontext.String("snapshotter"))
		if err == nil {
			fmt.Println("done")
		}
		return err
	},
}
