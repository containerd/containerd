package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	contentapi "github.com/docker/containerd/api/services/content"
	"github.com/docker/containerd/images"
	"github.com/docker/containerd/log"
	"github.com/docker/containerd/progress"
	contentservice "github.com/docker/containerd/services/content"
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
		var (
			ctx = background
		)

		db, err := getDB(clicontext, true)
		if err != nil {
			return errors.Wrap(err, "failed to open database")
		}
		tx, err := db.Begin(false)
		if err != nil {
			return errors.Wrap(err, "could not start transaction")
		}
		defer tx.Rollback()

		conn, err := connectGRPC(clicontext)
		if err != nil {
			return err
		}
		provider := contentservice.NewProviderFromClient(contentapi.NewContentClient(conn))

		images, err := images.List(tx)
		if err != nil {
			return errors.Wrap(err, "failed to list images")
		}

		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, ' ', 0)
		fmt.Fprintln(tw, "REF\tTYPE\tDIGEST\tSIZE\t")
		for _, image := range images {
			size, err := image.Size(ctx, provider)
			if err != nil {
				log.G(ctx).WithError(err).Errorf("failed calculating size for image %s", image.Name)
			}

			fmt.Fprintf(tw, "%v\t%v\t%v\t%v\t\n", image.Name, image.Descriptor.MediaType, image.Descriptor.Digest, progress.Bytes(size))
		}
		tw.Flush()

		return nil
	},
}

var rmiCommand = cli.Command{
	Name:        "rmi",
	Usage:       "Delete one or more images by reference.",
	ArgsUsage:   "[flags] <ref> [<ref>, ...]",
	Description: `Delete one or more images by reference.`,
	Flags:       []cli.Flag{},
	Action: func(clicontext *cli.Context) error {
		var (
			ctx     = background
			exitErr error
		)

		db, err := getDB(clicontext, false)
		if err != nil {
			return errors.Wrap(err, "failed to open database")
		}

		tx, err := db.Begin(true)
		if err != nil {
			return errors.Wrap(err, "could not start transaction")
		}
		defer tx.Rollback()

		for _, target := range clicontext.Args() {
			if err := images.Delete(tx, target); err != nil {
				if exitErr == nil {
					exitErr = errors.Wrapf(err, "unable to delete %v", target)
				}
				log.G(ctx).WithError(err).Errorf("unable to delete %v", target)
			}

			fmt.Println(target)
		}

		return exitErr
	},
}
