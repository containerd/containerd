package main

import (
	"context"
	"encoding/json"
	"os"
	"text/tabwriter"
	"time"

	contentapi "github.com/containerd/containerd/api/services/content"
	rootfsapi "github.com/containerd/containerd/api/services/rootfs"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/progress"
	"github.com/containerd/containerd/remotes"
	contentservice "github.com/containerd/containerd/services/content"
	rootfsservice "github.com/containerd/containerd/services/rootfs"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli"
	"golang.org/x/sync/errgroup"
)

var pullCommand = cli.Command{
	Name:      "pull",
	Usage:     "pull an image from a remote",
	ArgsUsage: "[flags] <ref>",
	Description: `Fetch and prepare an image for use in containerd.

After pulling an image, it should be ready to use the same reference in a run
command. As part of this process, we do the following:

1. Fetch all resources into containerd.
2. Prepare the snapshot filesystem with the pulled resources.
3. Register metadata for the image.
`,
	Flags: registryFlags,
	Action: func(clicontext *cli.Context) error {
		var (
			ctx = background
			ref = clicontext.Args().First()
		)

		conn, err := connectGRPC(clicontext)
		if err != nil {
			return err
		}

		imageStore, err := resolveImageStore(clicontext)
		if err != nil {
			return err
		}

		resolver, err := getResolver(ctx, clicontext)
		if err != nil {
			return err
		}
		ongoing := newJobs()

		// TODO(stevvooe): Must unify this type.
		ingester := contentservice.NewIngesterFromClient(contentapi.NewContentClient(conn))
		provider := contentservice.NewProviderFromClient(contentapi.NewContentClient(conn))

		cs, err := resolveContentStore(clicontext)
		if err != nil {
			return err
		}

		eg, ctx := errgroup.WithContext(ctx)

		var resolvedImageName string
		resolved := make(chan struct{})
		eg.Go(func() error {
			ongoing.add(ref)
			name, desc, fetcher, err := resolver.Resolve(ctx, ref)
			if err != nil {
				log.G(ctx).WithError(err).Error("failed to resolve")
				return err
			}
			log.G(ctx).WithField("image", name).Debug("fetching")
			resolvedImageName = name
			close(resolved)

			eg.Go(func() error {
				return imageStore.Put(ctx, name, desc)
			})

			return images.Dispatch(ctx,
				images.Handlers(images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
					ongoing.add(remotes.MakeRefKey(ctx, desc))
					return nil, nil
				}),
					remotes.FetchHandler(ingester, fetcher),
					images.ChildrenHandler(provider)),
				desc)

		})

		errs := make(chan error)
		go func() {
			defer close(errs)
			errs <- eg.Wait()
		}()

		defer func() {
			ctx := background

			// TODO(stevvooe): This section unpacks the layers and resolves the
			// root filesystem chainid for the image. For now, we just print
			// it, but we should keep track of this in the metadata storage.

			image, err := imageStore.Get(ctx, resolvedImageName)
			if err != nil {
				log.G(ctx).Fatal(err)
			}

			provider := contentservice.NewProviderFromClient(contentapi.NewContentClient(conn))

			p, err := content.ReadBlob(ctx, provider, image.Target.Digest)
			if err != nil {
				log.G(ctx).Fatal(err)
			}

			var manifest ocispec.Manifest
			if err := json.Unmarshal(p, &manifest); err != nil {
				log.G(ctx).Fatal(err)
			}

			rootfs := rootfsservice.NewUnpackerFromClient(rootfsapi.NewRootFSClient(conn))

			log.G(ctx).Info("unpacking rootfs")
			chainID, err := rootfs.Unpack(ctx, manifest.Layers)
			if err != nil {
				log.G(ctx).Fatal(err)
			}

			diffIDs, err := image.RootFS(ctx, provider)
			if err != nil {
				log.G(ctx).WithError(err).Fatal("failed resolving rootfs")
			}

			expectedChainID := identity.ChainID(diffIDs)
			if expectedChainID != chainID {
				log.G(ctx).Fatal("rootfs service did not match chainid")
			}
		}()

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
				js := ongoing.jobs()
				statuses := map[string]statusInfo{}

				activeSeen := map[string]struct{}{}
				if !done {
					active, err := cs.Active()
					if err != nil {
						log.G(ctx).WithError(err).Error("active check failed")
						continue
					}
					// update status of active entries!
					for _, active := range active {
						statuses[active.Ref] = statusInfo{
							Ref:       active.Ref,
							Status:    "downloading",
							Offset:    active.Offset,
							Total:     active.Total,
							StartedAt: active.StartedAt,
							UpdatedAt: active.UpdatedAt,
						}
						activeSeen[active.Ref] = struct{}{}
					}
				}

				// now, update the items in jobs that are not in active
				for _, j := range js {
					if _, ok := activeSeen[j]; ok {
						continue
					}
					status := "done"

					if j == ref {
						select {
						case <-resolved:
							status = "resolved"
						default:
							status = "resolving"
						}
					}

					statuses[j] = statusInfo{
						Ref:    j,
						Status: status, // for now!
					}
				}

				var ordered []statusInfo
				for _, j := range js {
					ordered = append(ordered, statuses[j])
				}

				display(tw, ordered, start)
				tw.Flush()

				if done {
					fw.Flush()
					return nil
				}
			case err := <-errs:
				if err != nil {
					return err
				}
				done = true
			case <-ctx.Done():
				done = true // allow ui to update once more
			}
		}

	},
}
