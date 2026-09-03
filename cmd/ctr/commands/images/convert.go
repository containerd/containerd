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
	"errors"
	"fmt"
	"strings"

	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/core/images/converter"
	"github.com/containerd/containerd/v2/core/images/converter/erofs"
	"github.com/containerd/containerd/v2/core/images/converter/uncompress"
	"github.com/containerd/platforms"
	"github.com/urfave/cli/v2"
)

var convertCommand = &cli.Command{
	Name:      "convert",
	Usage:     "Convert an image",
	ArgsUsage: "[flags] <source_ref> <target_ref>",
	Description: `Convert an image format.

e.g., 'ctr image convert --uncompress --oci example.com/foo:orig example.com/foo:converted'
      'ctr image convert --erofs raw example.com/foo:orig example.com/foo:erofs'
      'ctr image convert --erofs zstd example.com/foo:orig example.com/foo:erofs-zstd'

Use '--platform' to define the output platform.
When '--all-platforms' is given all images in a manifest list must be available.
`,
	Flags: []cli.Flag{
		// generic flags
		&cli.BoolFlag{
			Name:  "uncompress",
			Usage: "Convert tar.gz layers to uncompressed tar layers",
		},
		&cli.BoolFlag{
			Name:  "oci",
			Usage: "Convert Docker media types to OCI media types",
		},
		// erofs flags
		&cli.StringFlag{
			Name:  "erofs",
			Usage: "Convert layers to EROFS format, must specify 'raw' or 'zstd' (e.g. --erofs raw, --erofs zstd)",
		},
		&cli.StringFlag{
			Name:  "erofs-compressors",
			Usage: "Specify compression algorithm list when converting EROFS layers",
		},
		&cli.StringFlag{
			Name:  "erofs-mkfs-options",
			Usage: "Extra mkfs options applied when converting EROFS layers. (e.g. '-Efragments,dedupe')",
		},
		// platform flags
		&cli.StringSliceFlag{
			Name:  "platform",
			Usage: "Pull content from a specific platform",
		},
		&cli.BoolFlag{
			Name:  "all-platforms",
			Usage: "Exports content from all platforms",
		},
	},
	Action: func(cmd *cli.Context) error {
		var convertOpts []converter.Opt
		srcRef := cmd.Args().Get(0)
		targetRef := cmd.Args().Get(1)
		if srcRef == "" || targetRef == "" {
			return errors.New("src and target image need to be specified")
		}

		if !cmd.Bool("all-platforms") {
			if pss := cmd.StringSlice("platform"); len(pss) > 0 {
				all, err := platforms.ParseAll(pss)
				if err != nil {
					return err
				}
				convertOpts = append(convertOpts, converter.WithPlatform(platforms.Ordered(all...)))
			} else {
				convertOpts = append(convertOpts, converter.WithPlatform(platforms.DefaultStrict()))
			}
		}

		if cmd.Bool("uncompress") {
			convertOpts = append(convertOpts, converter.WithLayerConvertFunc(uncompress.LayerConvertFunc))
		}

		if cmd.IsSet("erofs") {
			var erofsOpts []erofs.ConvertOpt
			switch cmd.String("erofs") {
			case "raw":
			case "zstd":
				erofsOpts = append(erofsOpts, erofs.WithBlobCompression("zstd"))
			default:
				return fmt.Errorf("unsupported erofs format %q, supported: raw, zstd", cmd.String("erofs"))
			}
			if compressors := cmd.String("erofs-compressors"); compressors != "" {
				erofsOpts = append(erofsOpts, erofs.WithCompressors(compressors))
			}
			if mkfsOptsStr := cmd.String("erofs-mkfs-options"); mkfsOptsStr != "" {
				mkfsOpts := strings.Fields(mkfsOptsStr)
				erofsOpts = append(erofsOpts, erofs.WithMkfsOptions(mkfsOpts))
			}
			convertOpts = append(convertOpts, converter.WithLayerConvertFunc(erofs.LayerConvertFunc(erofsOpts...)))
			convertOpts = append(convertOpts, converter.WithUpdateManifest(erofs.UpdateManifestPlatform))
		}

		if cmd.Bool("oci") {
			convertOpts = append(convertOpts, converter.WithDockerToOCI(true))
		}

		client, ctx, cancel, err := commands.NewClient(cmd)
		if err != nil {
			return err
		}
		defer cancel()

		newImg, err := converter.Convert(ctx, client, targetRef, srcRef, convertOpts...)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.App.Writer, newImg.Target.Digest.String())
		return nil
	},
}
