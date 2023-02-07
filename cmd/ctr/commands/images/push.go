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
	gocontext "context"
	"errors"
	"fmt"
	"net/http/httptrace"
	"os"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/cmd/ctr/commands/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/progress"
	"github.com/containerd/containerd/pkg/transfer"
	"github.com/containerd/containerd/pkg/transfer/image"
	"github.com/containerd/containerd/pkg/transfer/registry"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli"
	"golang.org/x/sync/errgroup"
)

var pushCommand = cli.Command{
	Name:      "push",
	Usage:     "push an image to a remote",
	ArgsUsage: "[flags] <remote> [<local>]",
	Description: `Pushes an image reference from containerd.

	All resources associated with the manifest reference will be pushed.
	The ref is used to resolve to a locally existing image manifest.
	The image manifest must exist before push. Creating a new image
	manifest can be done through calculating the diff for layers,
	creating the associated configuration, and creating the manifest
	which references those resources.
`,
	Flags: append(commands.RegistryFlags, cli.StringFlag{
		Name:  "manifest",
		Usage: "Digest of manifest",
	}, cli.StringFlag{
		Name:  "manifest-type",
		Usage: "Media type of manifest digest",
		Value: ocispec.MediaTypeImageManifest,
	}, cli.StringSliceFlag{
		Name:  "platform",
		Usage: "Push content from a specific platform",
		Value: &cli.StringSlice{},
	}, cli.IntFlag{
		Name:  "max-concurrent-uploaded-layers",
		Usage: "Set the max concurrent uploaded layers for each push",
	}, cli.BoolTFlag{
		Name:  "local",
		Usage: "Push content from local client rather than using transfer service",
	}, cli.BoolFlag{
		Name:  "allow-non-distributable-blobs",
		Usage: "Allow pushing blobs that are marked as non-distributable",
	}),
	Action: func(context *cli.Context) error {
		var (
			ref   = context.Args().First()
			local = context.Args().Get(1)
			debug = context.GlobalBool("debug")
			desc  ocispec.Descriptor
		)
		if ref == "" {
			return errors.New("please provide a remote image reference to push")
		}

		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		if !context.BoolT("local") {
			ch, err := commands.NewStaticCredentials(ctx, context, ref)
			if err != nil {
				return err
			}

			if local == "" {
				local = ref
			}
			reg := registry.NewOCIRegistry(ref, nil, ch)
			is := image.NewStore(local)

			pf, done := ProgressHandler(ctx, os.Stdout)
			defer done()

			return client.Transfer(ctx, is, reg, transfer.WithProgress(pf))
		}

		if manifest := context.String("manifest"); manifest != "" {
			desc.Digest, err = digest.Parse(manifest)
			if err != nil {
				return fmt.Errorf("invalid manifest digest: %w", err)
			}
			desc.MediaType = context.String("manifest-type")
		} else {
			if local == "" {
				local = ref
			}
			img, err := client.ImageService().Get(ctx, local)
			if err != nil {
				return fmt.Errorf("unable to resolve image to manifest: %w", err)
			}
			desc = img.Target

			if pss := context.StringSlice("platform"); len(pss) == 1 {
				p, err := platforms.Parse(pss[0])
				if err != nil {
					return fmt.Errorf("invalid platform %q: %w", pss[0], err)
				}

				cs := client.ContentStore()
				if manifests, err := images.Children(ctx, cs, desc); err == nil && len(manifests) > 0 {
					matcher := platforms.NewMatcher(p)
					for _, manifest := range manifests {
						if manifest.Platform != nil && matcher.Match(*manifest.Platform) {
							if _, err := images.Children(ctx, cs, manifest); err != nil {
								return fmt.Errorf("no matching manifest: %w", err)
							}
							desc = manifest
							break
						}
					}
				}
			}
		}

		if context.Bool("http-trace") {
			ctx = httptrace.WithClientTrace(ctx, commands.NewDebugClientTrace(ctx))
		}
		resolver, err := commands.GetResolver(ctx, context)
		if err != nil {
			return err
		}
		ongoing := newPushJobs(commands.PushTracker)

		eg, ctx := errgroup.WithContext(ctx)

		// used to notify the progress writer
		doneCh := make(chan struct{})

		eg.Go(func() error {
			defer close(doneCh)

			log.G(ctx).WithField("image", ref).WithField("digest", desc.Digest).Debug("pushing")

			jobHandler := images.HandlerFunc(func(ctx gocontext.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				if !context.Bool("allow-non-distributable-blobs") && images.IsNonDistributable(desc.MediaType) {
					return nil, nil
				}
				ongoing.add(remotes.MakeRefKey(ctx, desc))
				return nil, nil
			})

			handler := jobHandler
			if !context.Bool("allow-non-distributable-blobs") {
				handler = remotes.SkipNonDistributableBlobs(handler)
			}

			ropts := []containerd.RemoteOpt{
				containerd.WithResolver(resolver),
				containerd.WithImageHandler(handler),
			}

			if context.IsSet("max-concurrent-uploaded-layers") {
				mcu := context.Int("max-concurrent-uploaded-layers")
				ropts = append(ropts, containerd.WithMaxConcurrentUploadedLayers(mcu))
			}

			return client.Push(ctx, ref, desc, ropts...)
		})

		// don't show progress if debug mode is set
		if !debug {
			eg.Go(func() error {
				var (
					ticker = time.NewTicker(100 * time.Millisecond)
					fw     = progress.NewWriter(os.Stdout)
					start  = time.Now()
					done   bool
				)

				defer ticker.Stop()

				for {
					select {
					case <-ticker.C:
						fw.Flush()

						tw := tabwriter.NewWriter(fw, 1, 8, 1, ' ', 0)

						content.Display(tw, ongoing.status(), start)
						tw.Flush()

						if done {
							fw.Flush()
							return nil
						}
					case <-doneCh:
						done = true
					case <-ctx.Done():
						done = true // allow ui to update once more
					}
				}
			})
		}
		return eg.Wait()
	},
}

type pushjobs struct {
	jobs    map[string]struct{}
	ordered []string
	tracker docker.StatusTracker
	mu      sync.Mutex
}

func newPushJobs(tracker docker.StatusTracker) *pushjobs {
	return &pushjobs{
		jobs:    make(map[string]struct{}),
		tracker: tracker,
	}
}

func (j *pushjobs) add(ref string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if _, ok := j.jobs[ref]; ok {
		return
	}
	j.ordered = append(j.ordered, ref)
	j.jobs[ref] = struct{}{}
}

func (j *pushjobs) status() []content.StatusInfo {
	j.mu.Lock()
	defer j.mu.Unlock()

	statuses := make([]content.StatusInfo, 0, len(j.jobs))
	for _, name := range j.ordered {
		si := content.StatusInfo{
			Ref: name,
		}

		status, err := j.tracker.GetStatus(name)
		if err != nil {
			si.Status = content.StatusWaiting
		} else {
			si.Offset = status.Offset
			si.Total = status.Total
			si.StartedAt = status.StartedAt
			si.UpdatedAt = status.UpdatedAt
			if status.Offset >= status.Total {
				if status.UploadUUID == "" {
					si.Status = content.StatusDone
				} else {
					si.Status = content.StatusCommitting
				}
			} else {
				si.Status = content.StatusUploading
			}
		}
		statuses = append(statuses, si)
	}

	return statuses
}
