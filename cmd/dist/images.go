package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	contentapi "github.com/containerd/containerd/api/services/content"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/progress"
	contentservice "github.com/containerd/containerd/services/content"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var imagesListCommand = cli.Command{
	Name:        "list",
	Aliases:     []string{"images"},
	Usage:       "list images known to containerd",
	ArgsUsage:   "[flags] <ref>",
	Description: `List images registered with containerd.`,
	Flags:       []cli.Flag{},
	Action: func(clicontext *cli.Context) error {
		var (
			ctx = background
		)

		imageStore, err := resolveImageStore(clicontext)
		if err != nil {
			return err
		}

		conn, err := connectGRPC(clicontext)
		if err != nil {
			return err
		}

		provider := contentservice.NewProviderFromClient(contentapi.NewContentClient(conn))

		images, err := imageStore.List(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to list images")
		}

		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, ' ', 0)
		fmt.Fprintln(tw, "REF\tTYPE\tDIGEST\tSIZE\t")
		for _, image := range images {
			image.Provider = provider
			size, err := image.Size(ctx)
			if err != nil {
				log.G(ctx).WithError(err).Errorf("failed calculating size for image %s", image.Name)
			}

			fmt.Fprintf(tw, "%v\t%v\t%v\t%v\t\n", image.Name, image.Target.MediaType, image.Target.Digest, progress.Bytes(size))
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

		imageStore, err := resolveImageStore(clicontext)
		if err != nil {
			return err
		}

		for _, target := range clicontext.Args() {
			if err := imageStore.Delete(ctx, target); err != nil {
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
