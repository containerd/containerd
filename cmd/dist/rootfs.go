package main

import (
	"fmt"
	"os"
	"strings"

	diffapi "github.com/containerd/containerd/api/services/diff"
	snapshotapi "github.com/containerd/containerd/api/services/snapshot"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/rootfs"
	diffservice "github.com/containerd/containerd/services/diff"
	snapshotservice "github.com/containerd/containerd/services/snapshot"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
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
		ctx, cancel := appContext()
		defer cancel()

		dgst, err := digest.Parse(clicontext.Args().First())
		if err != nil {
			return err
		}

		log.G(ctx).Infof("unpacking layers from manifest %s", dgst.String())

		cs, err := resolveContentStore(clicontext)
		if err != nil {
			return err
		}

		image := images.Image{
			Target: ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageManifest,
				Digest:    dgst,
			},
		}

		layers, err := getImageLayers(ctx, image, cs)
		if err != nil {
			log.G(ctx).WithError(err).Fatal("Failed to get rootfs layers")
		}

		conn, err := connectGRPC(clicontext)
		if err != nil {
			log.G(ctx).Fatal(err)
		}

		snapshotter := snapshotservice.NewSnapshotterFromClient(snapshotapi.NewSnapshotClient(conn))
		applier := diffservice.NewDiffServiceFromClient(diffapi.NewDiffClient(conn))

		chainID, err := rootfs.ApplyLayers(ctx, layers, snapshotter, applier)
		if err != nil {
			return err
		}

		log.G(ctx).Infof("chain ID: %s", chainID.String())

		return nil
	},
}

var rootfsPrepareCommand = cli.Command{
	Name:      "prepare",
	Usage:     "prepare gets mount commands for digest",
	ArgsUsage: "[flags] <digest> <target>",
	Flags:     []cli.Flag{},
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext()
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

		conn, err := connectGRPC(clicontext)
		if err != nil {
			return err
		}

		snapshotter := snapshotservice.NewSnapshotterFromClient(snapshotapi.NewSnapshotClient(conn))

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
