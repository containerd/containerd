package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/containerd/containerd/log"
	digest "github.com/opencontainers/go-digest"
	"github.com/urfave/cli"
)

var rootfsCommand = cli.Command{
	Name:  "rootfs",
	Usage: "rootfs setups a rootfs",
	Subcommands: []cli.Command{
		rootfsUnpackCommand,
		rootfsPrepareCommand,
	},
}

var rootfsUnpackCommand = cli.Command{
	Name:      "unpack",
	Usage:     "unpack applies layers from a manifest to a snapshot",
	ArgsUsage: "[flags] <digest>",
	Flags:     []cli.Flag{},
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		dgst, err := digest.Parse(clicontext.Args().First())
		if err != nil {
			return err
		}

		log.G(ctx).Debugf("unpacking layers from manifest %s", dgst.String())

		client, err := getClient(clicontext)
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
				if err := image.Unpack(ctx); err != nil {
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

var rootfsPrepareCommand = cli.Command{
	Name:      "prepare",
	Usage:     "prepare gets mount commands for digest",
	ArgsUsage: "[flags] <digest> <target>",
	Flags:     []cli.Flag{},
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		if clicontext.NArg() != 2 {
			return cli.ShowSubcommandHelp(clicontext)
		}

		dgst, err := digest.Parse(clicontext.Args().Get(0))
		if err != nil {
			return err
		}
		target := clicontext.Args().Get(1)

		log.G(ctx).Infof("preparing mounts %s", dgst.String())

		client, err := getClient(clicontext)
		if err != nil {
			return err
		}

		snapshotter := client.SnapshotService()
		mounts, err := snapshotter.Prepare(ctx, target, dgst.String())
		if err != nil {
			return err
		}

		for _, m := range mounts {
			fmt.Fprintf(os.Stdout, "mount -t %s %s %s -o %s\n", m.Type, m.Source, target, strings.Join(m.Options, ","))
		}

		return nil
	},
}
