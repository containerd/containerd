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
	"github.com/containerd/containerd/v2/internal/dmverity"
	"github.com/containerd/containerd/v2/internal/erofsutils"
)

var buildErofsCacheCommand = &cli.Command{
	Name:      "build-erofs-cache",
	Usage:     "Build an erofs layer content cache from an image's layers",
	ArgsUsage: "[flags] <image_ref> <cache_dir>",
	Description: `Convert each layer of an already-pulled image into a directory of
diffID-keyed erofs blobs (<cache_dir>/<algorithm>/<xx>/<hex>.erofs, where <xx> is
the first two characters of <hex>) for the erofs
snapshotter's layer_content_cache. Layers are read from the content store; no
converted image is produced. The directory can then be synced to the read-only
location the fleet mounts. Requires mkfs.erofs.

Pass --dmverity to also generate a dm-verity hash tree and .dmverity sidecar for
each blob (Linux only); this is required when the snapshotter runs with
dmverity_mode=on, which rejects cache hits that lack a sidecar.`,
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
		&cli.BoolFlag{
			Name:  "dmverity",
			Usage: "Generate a dm-verity hash tree and .dmverity sidecar for each blob (Linux only)",
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

		if err := buildCache(ctx, client.ContentStore(), img.Target, platform, cacheDir, cliContext.Bool("dmverity"), opts...); err != nil {
			if errdefs.IsNotFound(err) {
				return fmt.Errorf("fetch the image content first, e.g. `ctr content fetch %s`: %w", ref, err)
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
// and layers shared across images converge on one blob. When withDmverity is set
// a dm-verity hash tree and .dmverity sidecar are also produced per blob.
func buildCache(ctx context.Context, cs content.Store, image ocispec.Descriptor, platform platforms.MatchComparer, cacheDir string, withDmverity bool, opts ...erofs.ConvertOpt) error {
	manifest, err := images.Manifest(ctx, cs, image, platform)
	if err != nil {
		return fmt.Errorf("failed to resolve manifest: %w", err)
	}

	for _, layer := range manifest.Layers {
		if !images.IsLayerType(layer.MediaType) || erofsutils.IsErofsMediaType(layer.MediaType) || images.IsNonDistributable(layer.MediaType) {
			continue
		}
		if err := buildLayer(ctx, cs, layer, cacheDir, withDmverity, opts...); err != nil {
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
// operation idempotent across re-runs and shared layers. When withDmverity is
// set the blob is dm-verity formatted and its sidecar is moved into place too.
func buildLayer(ctx context.Context, cs content.Store, layer ocispec.Descriptor, cacheDir string, withDmverity bool, opts ...erofs.ConvertOpt) (retErr error) {
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
			os.Remove(dmverity.MetadataPath(tmpPath))
		}
	}()

	diffID, err := erofs.ConvertLayerToErofs(ctx, cs, layer, tmpPath, opts...)
	if err != nil {
		return err
	}

	if withDmverity {
		// Appends the hash tree to the blob and writes tmpPath's .dmverity sidecar.
		if err := dmverity.FormatLayer(ctx, tmpPath, nil); err != nil {
			return err
		}
	}

	// os.CreateTemp makes the file 0600; widen it so other users (e.g. a
	// rootless containerd) can read the shared cache.
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return err
	}

	dest := erofsutils.CacheBlobPath(cacheDir, diffID)
	destDir := filepath.Dir(dest)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	// fsync the temp files before renaming so a crash right after the rename can't
	// leave a zero-length or torn cache entry. A missing sidecar (dm-verity
	// disabled) is skipped.
	for _, p := range []string{tmpPath, dmverity.MetadataPath(tmpPath)} {
		if err := fsync(p); err != nil {
			return err
		}
	}

	// Move the sidecar into place before the blob: the snapshotter keys on the
	// blob's presence, so the sidecar must already exist when the blob appears.
	if withDmverity {
		if err := os.Rename(dmverity.MetadataPath(tmpPath), dmverity.MetadataPath(dest)); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return err
	}

	// fsync the directory so the renames themselves survive a crash.
	return fsync(destDir)
}

// fsync flushes the file (or directory) at path to disk. A path that does not
// exist is treated as a no-op so callers can fsync an optional sidecar.
func fsync(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
