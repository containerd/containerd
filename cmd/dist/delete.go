package main

import (
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	contentapi "github.com/containerd/containerd/api/services/content"
	"github.com/containerd/containerd/log"
	digest "github.com/opencontainers/go-digest"
	"github.com/urfave/cli"
)

var deleteCommand = cli.Command{
	Name:      "delete",
	Aliases:   []string{"del", "remove", "rm"},
	Usage:     "permanently delete one or more blobs.",
	ArgsUsage: "[flags] [<digest>, ...]",
	Description: `Delete one or more blobs permanently. Successfully deleted
	blobs are printed to stdout.`,
	Flags: []cli.Flag{},
	Action: func(context *cli.Context) error {
		var (
			args      = []string(context.Args())
			exitError error
		)
		ctx, cancel := appContext()
		defer cancel()

		conn, err := connectGRPC(context)
		if err != nil {
			return err
		}

		client := contentapi.NewContentClient(conn)

		for _, arg := range args {
			dgst, err := digest.Parse(arg)
			if err != nil {
				if exitError == nil {
					exitError = err
				}
				log.G(ctx).WithError(err).Errorf("could not delete %v", dgst)
				continue
			}

			if _, err := client.Delete(ctx, &contentapi.DeleteContentRequest{
				Digest: dgst,
			}); err != nil {
				switch grpc.Code(err) {
				case codes.NotFound:
					// if it is already deleted, ignore!
				default:
					if exitError == nil {
						exitError = err
					}
					log.G(ctx).WithError(err).Errorf("could not delete %v", dgst)
				}
				continue
			}

			fmt.Println(dgst)
		}

		return exitError
	},
}
