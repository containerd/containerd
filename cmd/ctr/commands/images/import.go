/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package images

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/images/archive"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/transfer"
	tarchive "github.com/containerd/containerd/pkg/transfer/archive"
	"github.com/containerd/containerd/pkg/transfer/image"
	"github.com/containerd/containerd/platforms"
	"github.com/urfave/cli"
)

var importCommand = cli.Command{
	Name:      "import",
	Usage:     "import images",
	ArgsUsage: "[flags] <in>",
	Description: `Import images from a tar stream.
Implemented formats:
- oci.v1
- docker.v1.1
- docker.v1.2


For OCI v1, you may need to specify --base-name because an OCI archive may
contain only partial image references (tags without the base image name).
If no base image name is provided, a name will be generated as "import-%{yyyy-MM-dd}".

e.g.
  $ ctr images import --base-name foo/bar foobar.tar

If foobar.tar contains an OCI ref named "latest" and anonymous ref "sha256:deadbeef", the command will create
"foo/bar:latest" and "foo/bar@sha256:deadbeef" images in the containerd store.
`,
	Flags: append([]cli.Flag{
		cli.StringFlag{
			Name:  "base-name",
			Value: "",
			Usage: "Base image name for added images, when provided only images with this name prefix are imported",
		},
		cli.BoolFlag{
			Name:  "digests",
			Usage: "Whether to create digest images (default: false)",
		},
		cli.BoolFlag{
			Name:  "skip-digest-for-named",
			Usage: "Skip applying --digests option to images named in the importing tar (use it in conjunction with --digests)",
		},
		cli.StringFlag{
			Name:  "index-name",
			Usage: "Image name to keep index as, by default index is discarded",
		},
		cli.BoolFlag{
			Name:  "all-platforms",
			Usage: "Imports content for all platforms, false by default",
		},
		cli.StringFlag{
			Name:  "platform",
			Usage: "Imports content for specific platform",
		},
		cli.BoolFlag{
			Name:  "no-unpack",
			Usage: "Skip unpacking the images, cannot be used with --discard-unpacked-layers, false by default",
		},
		cli.BoolTFlag{
			Name:  "local",
			Usage: "Run import locally rather than through transfer API",
		},
		cli.BoolFlag{
			Name:  "compress-blobs",
			Usage: "Compress uncompressed blobs when creating manifest (Docker format only)",
		},
		cli.BoolFlag{
			Name:  "discard-unpacked-layers",
			Usage: "Allow the garbage collector to clean layers up from the content store after unpacking, cannot be used with --no-unpack, false by default",
		},
	}, commands.SnapshotterFlags...),

	Action: func(context *cli.Context) error {
		var (
			in              = context.Args().First()
			opts            []containerd.ImportOpt
			platformMatcher platforms.MatchComparer
		)

		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		if !context.BoolT("local") {
			var opts []image.StoreOpt
			prefix := context.String("base-name")
			var overwrite bool
			if prefix == "" {
				prefix = fmt.Sprintf("import-%s", time.Now().Format("2006-01-02"))
				// Allow overwriting auto-generated prefix with named annotation
				overwrite = true
			}

			if context.Bool("digests") {
				opts = append(opts, image.WithDigestRef(prefix, overwrite, !context.Bool("skip-digest-for-named")))
			} else {
				opts = append(opts, image.WithNamedPrefix(prefix, overwrite))
			}

			// TODO: Add platform options

			// TODO: Add unpack options

			is := image.NewStore(context.String("index-name"), opts...)

			var iopts []tarchive.ImportOpt

			if context.Bool("compress-blobs") {
				iopts = append(iopts, tarchive.WithForceCompression)
			}

			var r io.ReadCloser
			if in == "-" {
				r = os.Stdin
			} else {
				var err error
				r, err = os.Open(in)
				if err != nil {
					return err
				}
			}
			iis := tarchive.NewImageImportStream(r, "", iopts...)

			pf, done := ProgressHandler(ctx, os.Stdout)
			defer done()

			err := client.Transfer(ctx, iis, is, transfer.WithProgress(pf))
			closeErr := r.Close()
			if err != nil {
				return err
			}

			return closeErr
		}

		// Local logic

		prefix := context.String("base-name")
		if prefix == "" {
			prefix = fmt.Sprintf("import-%s", time.Now().Format("2006-01-02"))
			opts = append(opts, containerd.WithImageRefTranslator(archive.AddRefPrefix(prefix)))
		} else {
			// When provided, filter out references which do not match
			opts = append(opts, containerd.WithImageRefTranslator(archive.FilterRefPrefix(prefix)))
		}

		if context.Bool("digests") {
			opts = append(opts, containerd.WithDigestRef(archive.DigestTranslator(prefix)))
		}
		if context.Bool("skip-digest-for-named") {
			if !context.Bool("digests") {
				return fmt.Errorf("--skip-digest-for-named must be specified with --digests option")
			}
			opts = append(opts, containerd.WithSkipDigestRef(func(name string) bool { return name != "" }))
		}

		if idxName := context.String("index-name"); idxName != "" {
			opts = append(opts, containerd.WithIndexName(idxName))
		}

		if context.Bool("compress-blobs") {
			opts = append(opts, containerd.WithImportCompression())
		}

		if platform := context.String("platform"); platform != "" {
			platSpec, err := platforms.Parse(platform)
			if err != nil {
				return err
			}
			platformMatcher = platforms.OnlyStrict(platSpec)
			opts = append(opts, containerd.WithImportPlatform(platformMatcher))
		}

		opts = append(opts, containerd.WithAllPlatforms(context.Bool("all-platforms")))

		if context.Bool("discard-unpacked-layers") {
			if context.Bool("no-unpack") {
				return fmt.Errorf("--discard-unpacked-layers and --no-unpack are incompatible options")
			}
			opts = append(opts, containerd.WithDiscardUnpackedLayers())
		}

		ctx, done, err := client.WithLease(ctx)
		if err != nil {
			return err
		}
		defer done(ctx)

		var r io.ReadCloser
		if in == "-" {
			r = os.Stdin
		} else {
			r, err = os.Open(in)
			if err != nil {
				return err
			}
		}

		imgs, err := client.Import(ctx, r, opts...)
		closeErr := r.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}

		if !context.Bool("no-unpack") {
			log.G(ctx).Debugf("unpacking %d images", len(imgs))

			for _, img := range imgs {
				if platformMatcher == nil { // if platform not specified use default.
					platformMatcher = platforms.Default()
				}
				image := containerd.NewImageWithPlatform(client, img, platformMatcher)

				// TODO: Show unpack status
				fmt.Printf("unpacking %s (%s)...", img.Name, img.Target.Digest)
				err = image.Unpack(ctx, context.String("snapshotter"))
				if err != nil {
					return err
				}
				fmt.Println("done")
			}
		}
		return nil
	},
}
