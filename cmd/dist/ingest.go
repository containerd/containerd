package main

import (
	contextpkg "context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/containerd/content"
	"github.com/opencontainers/go-digest"
	"github.com/urfave/cli"
	"github.com/pkg/errors"
)

var ingestCommand = cli.Command{
	Name:        "ingest",
	Usage:       "accept content into the store",
	ArgsUsage:   "[flags] <key>",
	Description: `Ingest objects into the local content store.`,
	Flags: []cli.Flag{
		cli.DurationFlag{
			Name:   "timeout",
			Usage:  "total timeout for fetch",
			EnvVar: "CONTAINERD_FETCH_TIMEOUT",
		},
		cli.StringFlag{
			Name:   "path, p",
			Usage:  "path to content store",
			Value:  ".content", // TODO(stevvooe): for now, just use the PWD/.content
			EnvVar: "CONTAINERD_DIST_CONTENT_STORE",
		},
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
			ctx            = contextpkg.Background()
			timeout        = context.Duration("timeout")
			root           = context.String("path")
			ref            = context.Args().First()
			expectedSize   = context.Int64("expected-size")
			expectedDigest = digest.Digest(context.String("expected-digest"))
		)

		if timeout > 0 {
			var cancel func()
			ctx, cancel = contextpkg.WithTimeout(ctx, timeout)
			defer cancel()
		}

		if err := expectedDigest.Validate(); expectedDigest != "" && err != nil {
			return err
		}

		if !filepath.IsAbs(root) {
			var err error
			root, err = filepath.Abs(root)
			if err != nil {
				return err
			}
		}

		cs, err := content.Open(root)
		if err != nil {
			return err
		}

		if expectedDigest != "" {
			if ok, err := cs.Exists(expectedDigest); err != nil {
				return err
			} else if ok {
				fmt.Fprintf(os.Stderr, "content with digest %v already exists\n", expectedDigest)
				return nil
			}
		}

		if ref == "" {
			if expectedDigest == "" {
				return errors.New("must specify a transaction reference or expected digest")
			}

			ref = strings.Replace(expectedDigest.String(), ":", "-", -1)
		}

		// TODO(stevvooe): Allow ingest to be reentrant. Currently, we expect
		// all data to be written in a single invocation. Allow multiple writes
		// to the same transaction key followed by a commit.
		return content.WriteBlob(cs, os.Stdin, ref, expectedSize, expectedDigest)
	},
}
