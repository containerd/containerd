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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli/v2"

	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/converter/erofs"
	"github.com/containerd/containerd/v2/internal/erofsutils"
)

var buildErofsCacheCommand = &cli.Command{
	Name:      "build-erofs-cache",
	Usage:     "Build an erofs layer content cache from an image's layers",
	ArgsUsage: "[flags] <image_ref> <cache_dir>",
	Description: `Convert each layer of an already-pulled image into a directory of
diffID-keyed erofs blobs (<cache_dir>/<algorithm>/<hex>.erofs) for the erofs
snapshotter's layer_content_cache. Layers are read from the content store; no
converted image is produced. The directory can then be synced to the read-only
location the fleet mounts. Requires mkfs.erofs.`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "compressors",
			Usage: "EROFS per-block compression algorithm(s), e.g. 'lz4', 'deflate', 'zstd'; empty means uncompressed",
		},
		&cli.StringFlag{
			Name:  "mkfs-options",
			Usage: "Extra mkfs.erofs options (e.g. '-Efragments,dedupe')",
		},
		&cli.StringFlag{
			Name:  "platform",
			Usage: "Cache layers for a specific platform (default: host platform)",
		},
	},
	Action: func(cliContext *cli.Context) error {
		ref := cliContext.Args().Get(0)
		cacheDir := cliContext.Args().Get(1)
		if ref == "" || cacheDir == "" {
			return errors.New("image ref and cache dir must be specified")
		}

		platform := platforms.DefaultStrict()
		if p := cliContext.String("platform"); p != "" {
			parsed, err := platforms.Parse(p)
			if err != nil {
				return fmt.Errorf("invalid platform %q: %w", p, err)
			}
			platform = platforms.Only(parsed)
		}

		var opts []erofs.ConvertOpt
		if c := cliContext.String("compressors"); c != "" {
			opts = append(opts, erofs.WithCompressors(c))
		}
		if m := cliContext.String("mkfs-options"); m != "" {
			opts = append(opts, erofs.WithMkfsOptions(strings.Fields(m)))
		}

		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		img, err := client.ImageService().Get(ctx, ref)
		if err != nil {
			return err
		}

		if err := buildCache(ctx, client.ContentStore(), img.Target, platform, cacheDir, opts...); err != nil {
			if errdefs.IsNotFound(err) {
				return fmt.Errorf("%w; fetch the image content first, e.g. `ctr content fetch %s`", err, ref)
			}
			return err
		}
		fmt.Fprintln(cliContext.App.Writer, cacheDir)
		return nil
	},
}

// buildCache converts each layer of image (for the given platform) into the
// erofs layer content cache at cacheDir, keyed by the layer's diffID. It reads
// layers straight from the content store — no converted image is produced — so
// the cache can be populated from an already-pulled image and then synced to the
// read-only location the fleet mounts. Because the key is the source layer's
// diffID, it matches what the runtime looks up when pulling the original image,
// and layers shared across images converge on one blob. dm-verity sidecars are
// not produced here (the converter does no dm-verity formatting).
func buildCache(ctx context.Context, cs content.Store, image ocispec.Descriptor, platform platforms.MatchComparer, cacheDir string, opts ...erofs.ConvertOpt) error {
	manifest, err := images.Manifest(ctx, cs, image, platform)
	if err != nil {
		return fmt.Errorf("failed to resolve manifest: %w", err)
	}

	for _, layer := range manifest.Layers {
		if !images.IsLayerType(layer.MediaType) || erofsutils.IsErofsMediaType(layer.MediaType) || images.IsNonDistributable(layer.MediaType) {
			continue
		}
		if err := buildLayer(ctx, cs, layer, cacheDir, opts...); err != nil {
			// Preserve errdefs.IsNotFound so callers can add context-appropriate
			// remediation (e.g. ctr suggesting `ctr content fetch`).
			if errdefs.IsNotFound(err) {
				return fmt.Errorf("layer %s is not in the content store: %w", layer.Digest, err)
			}
			return fmt.Errorf("failed to cache layer %s: %w", layer.Digest, err)
		}
	}
	return nil
}

// buildLayer converts one layer into a temp file inside cacheDir, then
// atomically renames it to its diffID-keyed name (same filesystem, so no extra
// copy and no reliance on TMPDIR). An existing entry is overwritten, keeping the
// operation idempotent across re-runs and shared layers.
func buildLayer(ctx context.Context, cs content.Store, layer ocispec.Descriptor, cacheDir string, opts ...erofs.ConvertOpt) (retErr error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(cacheDir, ".build-*.erofs")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer func() {
		if retErr != nil {
			os.Remove(tmpPath)
		}
	}()

	diffID, err := erofs.ConvertLayerToErofs(ctx, cs, layer, tmpPath, opts...)
	if err != nil {
		return err
	}

	// os.CreateTemp makes the file 0600; widen it so other users (e.g. a
	// rootless containerd) can read the shared cache.
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return err
	}

	dest := filepath.Join(cacheDir, diffID.Algorithm().String(), diffID.Encoded()+".erofs")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	return os.Rename(tmpPath, dest)
}
