package main

import (
	"io"
	"os"

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
		ctx, cancel := appContext()
		defer cancel()

		dgst, err := digest.Parse(context.Args().First())
		if err != nil {
			return err
		}

		cs, err := resolveContentStore(context)
		if err != nil {
			return err
		}

		rc, err := cs.Reader(ctx, dgst)
		if err != nil {
			return err
		}
		defer rc.Close()

		_, err = io.Copy(os.Stdout, rc)
		return err
	},
}
