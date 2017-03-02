package main

import (
	"fmt"
	"github.com/docker/containerd/content"
	"github.com/opencontainers/go-digest"
	"github.com/urfave/cli"
)

var ociCommand = cli.Command{
	Name:      "oci",
	Usage:     "create oci image layout",
	ArgsUsage: "[flags] <ref> <manifest-digest>",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "blob-root",
			Usage: "root path contains blobs",
		},
		cli.StringFlag{
			Name:  "path, p",
			Usage: "path to create oci layout",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			blobRoot = context.String("blob-root")
			path     = context.String("path")
			ref      = content.Ref(context.Args().First())
			args     = context.Args().Tail()
		)

		if blobRoot == "" {
			return fmt.Errorf("blob-root required")
		}
		if path == "" {
			return fmt.Errorf("path required")
		}

		if ref == "" {
			return fmt.Errorf("ref required")
		}

		if err := ref.Validate(); err != nil {
			return err
		}

		if len(args) == 0 {
			return fmt.Errorf("manifest-digest required")
		}
		dgst := digest.Digest(args[0])

		if err := dgst.Validate(); err != nil {
			return err
		}

		return content.CreateOCILayout(path, blobRoot, ref, dgst)
	},
}
