package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/docker/containerd/image"
	"github.com/docker/containerd/progress"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var imagesCommand = cli.Command{
	Name:        "images",
	Usage:       "list images known to containerd",
	ArgsUsage:   "[flags] <ref>",
	Description: `List images registered with containerd.`,
	Flags:       []cli.Flag{},
	Action: func(clicontext *cli.Context) error {
		db, err := getDB(clicontext, true)
		if err != nil {
			return errors.Wrap(err, "failed to open database")
		}

		tx, err := db.Begin(false)
		if err != nil {
			return errors.Wrap(err, "could not start transaction")
		}
		defer tx.Rollback()

		images, err := image.List(tx)
		if err != nil {
			return errors.Wrap(err, "failed to list images")
		}

		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, ' ', 0)
		fmt.Fprintln(tw, "REF\tTYPE\tDIGEST\tSIZE\t")
		for _, image := range images {
			fmt.Fprintf(tw, "%v\t%v\t%v\t%v\t\n", image.Name, image.Descriptor.MediaType, image.Descriptor.Digest, progress.Bytes(image.Descriptor.Size))
		}
		tw.Flush()

		return nil
	},
}
