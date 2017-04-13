package main

import (
	"io"
	"os"

	contentapi "github.com/containerd/containerd/api/services/content"
	contentservice "github.com/containerd/containerd/services/content"
	digest "github.com/opencontainers/go-digest"
	"github.com/urfave/cli"
)

var getCommand = cli.Command{
	Name:        "get",
	Usage:       "get the data for an object",
	ArgsUsage:   "[flags] [<digest>, ...]",
	Description: "Display the image object.",
	Flags:       []cli.Flag{},
	Action: func(context *cli.Context) error {
		var (
			ctx = background
		)

		dgst, err := digest.Parse(context.Args().First())
		if err != nil {
			return err
		}

		conn, err := connectGRPC(context)
		if err != nil {
			return err
		}

		cs := contentservice.NewProviderFromClient(contentapi.NewContentClient(conn))

		rc, err := cs.Reader(ctx, dgst)
		if err != nil {
			return err
		}
		defer rc.Close()

		_, err = io.Copy(os.Stdout, rc)
		return err
	},
}
