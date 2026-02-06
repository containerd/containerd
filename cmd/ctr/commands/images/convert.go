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
	"github.com/containerd/containerd/v2/internal/erofsutils/seekable"
	"github.com/containerd/platforms"
	"github.com/urfave/cli/v2"
)

var convertCommand = &cli.Command{
	Name:      "convert",
	Usage:     "Convert an image",
	ArgsUsage: "[flags] <source_ref> <target_ref>",
	Description: `Convert an image format.

e.g., 'ctr images convert --uncompress --oci example.com/foo:orig example.com/foo:converted'
      'ctr images convert --erofs example.com/foo:orig example.com/foo:erofs'
      'ctr images convert --erofs --erofs-compression='lzma:lz4hc,12' example.com/foo:orig example.com/foo:erofs'
      'ctr images convert --erofs-seekable example.com/foo:orig example.com/foo:seekable-erofs'
      'ctr images convert --erofs-seekable --erofs-dm-verity example.com/foo:orig example.com/foo:seekable-erofs-verity'

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
		&cli.BoolFlag{
			Name:  "erofs",
			Usage: "Convert layers to EROFS format",
		},
		&cli.StringFlag{
			Name:  "erofs-compression",
			Usage: "Compression algorithms for EROFS, separated by colon (e.g., 'lz4:lz4hc,12')",
		},
		&cli.StringFlag{
			Name:  "erofs-mkfs-opts",
			Usage: "Extra options for mkfs.erofs (e.g., '-zlz4')",
		},
		// seekable erofs flags
		&cli.BoolFlag{
			Name:  "erofs-seekable",
			Usage: "Wrap EROFS in seekable zstd frames with custom chunk table",
		},
		&cli.IntFlag{
			Name:  "erofs-chunk-size",
			Usage: "Uncompressed bytes per zstd frame (random access granularity)",
			Value: seekable.DefaultChunkSize,
		},
		&cli.BoolFlag{
			Name:  "erofs-dm-verity",
			Usage: "Include dm-verity payload at EOF",
		},
		&cli.IntFlag{
			Name:  "erofs-dm-verity-block-size",
			Usage: "dm-verity block size in bytes",
			Value: seekable.DefaultDMVerityBlockSize,
		},
		// platform flags
		&cli.StringSliceFlag{
			Name:  "platform",
			Usage: "Pull content from a specific platform",
			Value: cli.NewStringSlice(),
		},
		&cli.BoolFlag{
			Name:  "all-platforms",
			Usage: "Exports content from all platforms",
		},
	},
	Action: func(cliContext *cli.Context) error {
		var convertOpts []converter.Opt
		srcRef := cliContext.Args().Get(0)
		targetRef := cliContext.Args().Get(1)
		if srcRef == "" || targetRef == "" {
			return errors.New("src and target image need to be specified")
		}

		if !cliContext.Bool("all-platforms") {
			if platformStrs := cliContext.StringSlice("platform"); len(platformStrs) > 0 {
				all, err := platforms.ParseAll(platformStrs)
				if err != nil {
					return err
				}
				convertOpts = append(convertOpts, converter.WithPlatform(platforms.Ordered(all...)))
			} else {
				convertOpts = append(convertOpts, converter.WithPlatform(platforms.DefaultStrict()))
			}
		}

		if cliContext.Bool("uncompress") {
			convertOpts = append(convertOpts, converter.WithLayerConvertFunc(uncompress.LayerConvertFunc))
		}

		if cliContext.Bool("erofs-seekable") {
			// Seekable EROFS: wrap raw EROFS in zstd frames with chunk table.
			var seekableOpts []erofs.SeekableConvertOpt
			if cliContext.IsSet("erofs-chunk-size") {
				seekableOpts = append(seekableOpts, erofs.WithSeekableChunkSize(cliContext.Int("erofs-chunk-size")))
			}
			seekableOpts = append(seekableOpts, erofs.WithSeekableDMVerity(cliContext.Bool("erofs-dm-verity")))
			if cliContext.IsSet("erofs-dm-verity-block-size") {
				seekableOpts = append(seekableOpts, erofs.WithSeekableDMVerityBlockSize(cliContext.Int("erofs-dm-verity-block-size")))
			}
			// Pass through raw EROFS options (compression, mkfs-opts).
			rawOpts := buildErofsOpts(cliContext)
			if len(rawOpts) > 0 {
				seekableOpts = append(seekableOpts, erofs.WithSeekableRawErofsOpts(rawOpts...))
			}
			convertOpts = append(convertOpts, converter.WithLayerConvertFunc(erofs.SeekableLayerConvertFunc(seekableOpts...)))
		} else if cliContext.Bool("erofs") {
			// Raw EROFS (non-seekable).
			erofsOpts := buildErofsOpts(cliContext)
			convertOpts = append(convertOpts, converter.WithLayerConvertFunc(erofs.LayerConvertFunc(erofsOpts...)))
		}

		if cliContext.Bool("oci") {
			convertOpts = append(convertOpts, converter.WithDockerToOCI(true))
		}

		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		newImg, err := converter.Convert(ctx, client, targetRef, srcRef, convertOpts...)
		if err != nil {
			return err
		}
		fmt.Fprintln(cliContext.App.Writer, newImg.Target.Digest.String())
		return nil
	},
}

func buildErofsOpts(cliContext *cli.Context) []erofs.ConvertOpt {
	var opts []erofs.ConvertOpt
	if compressors := cliContext.String("erofs-compression"); compressors != "" {
		opts = append(opts, erofs.WithCompressors(compressors))
	}
	if mkfsOptsStr := cliContext.String("erofs-mkfs-opts"); mkfsOptsStr != "" {
		mkfsOpts := strings.Fields(mkfsOptsStr)
		opts = append(opts, erofs.WithMkfsOptions(mkfsOpts))
	}
	return opts
}
