package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/containerd/containerd/rootfs"
	"github.com/urfave/cli"
)

var snapshotCommand = cli.Command{
	Name:      "snapshot",
	Usage:     "snapshot a container into an archive",
	ArgsUsage: "",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "id",
			Usage: "id of the container",
		},
	},
	Action: func(clicontext *cli.Context) error {
		id := clicontext.String("id")
		if id == "" {
			return errors.New("container id must be provided")
		}

		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

		differ, err := getDiffService(clicontext)
		if err != nil {
			return err
		}

		contentRef := fmt.Sprintf("diff-%s", id)

		d, err := rootfs.Diff(context.TODO(), id, contentRef, snapshotter, differ)
		if err != nil {
			return err
		}

		// TODO: Track progress
		fmt.Printf("%s %s\n", d.MediaType, d.Digest)

		return nil
	},
}
