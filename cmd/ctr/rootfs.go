package main

import (
	"errors"
	"fmt"

	"github.com/containerd/containerd/log"
	digest "github.com/opencontainers/go-digest"
	"github.com/urfave/cli"
)

var rootfsCommand = cli.Command{
	Name:  "rootfs",
	Usage: "setup a rootfs",
	Subcommands: []cli.Command{
		rootfsUnpackCommand,
	},
}

var rootfsUnpackCommand = cli.Command{
	Name:      "unpack",
	Usage:     "unpack applies layers from a manifest to a snapshot",
	ArgsUsage: "[flags] <digest>",
	Flags:     snapshotterFlags,
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		dgst, err := digest.Parse(clicontext.Args().First())
		if err != nil {
			return err
		}

		log.G(ctx).Debugf("unpacking layers from manifest %s", dgst.String())

		client, err := newClient(clicontext)
		if err != nil {
			return err
		}

		// TODO: Support unpack by name

		images, err := client.ListImages(ctx)
		if err != nil {
			return err
		}

		var unpacked bool
		for _, image := range images {
			if image.Target().Digest == dgst {
				fmt.Printf("unpacking %s (%s)...", dgst, image.Target().MediaType)
				if err := image.Unpack(ctx, clicontext.String("snapshotter")); err != nil {
					fmt.Println()
					return err
				}
				fmt.Println("done")
				unpacked = true
				break
			}
		}
		if !unpacked {
			return errors.New("manifest not found")
		}

		// TODO: Get rootfs from Image
		//log.G(ctx).Infof("chain ID: %s", chainID.String())

		return nil
	},
}
