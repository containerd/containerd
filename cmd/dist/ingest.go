package main

import (
	"os"

	"github.com/containerd/containerd/content"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var ingestCommand = cli.Command{
	Name:        "ingest",
	Usage:       "accept content into the store",
	ArgsUsage:   "[flags] <key>",
	Description: `Ingest objects into the local content store.`,
	Flags: []cli.Flag{
		cli.Int64Flag{
			Name:  "expected-size",
			Usage: "validate against provided size",
		},
		cli.StringFlag{
			Name:  "expected-digest",
			Usage: "verify content against expected digest",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			ref            = context.Args().First()
			expectedSize   = context.Int64("expected-size")
			expectedDigest = digest.Digest(context.String("expected-digest"))
		)

		ctx, cancel := appContext()
		defer cancel()

		if err := expectedDigest.Validate(); expectedDigest != "" && err != nil {
			return err
		}

		if ref == "" {
			return errors.New("must specify a transaction reference")
		}

		cs, err := resolveContentStore(context)
		if err != nil {
			return err
		}

		// TODO(stevvooe): Allow ingest to be reentrant. Currently, we expect
		// all data to be written in a single invocation. Allow multiple writes
		// to the same transaction key followed by a commit.
		return content.WriteBlob(ctx, cs, ref, os.Stdin, expectedSize, expectedDigest)
	},
}
