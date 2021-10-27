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

package ipfs

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/cmd/ctr/commands/content"
	"github.com/containerd/containerd/contrib/ipfs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	httpapi "github.com/ipfs/go-ipfs-http-client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var pullCommand = cli.Command{
	Name:      "pull",
	Usage:     "pull an image from IPFS",
	ArgsUsage: "[flags] <CID>",
	Description: `Fetch an image from IPFS and prepare for use in containerd.

<CID> is an IPFS CID file of the JSON-formatted OCI descriptor of the top-level blob of the target image (i.e. image index).

After pulling an image, it should be ready to use the same reference in a run
command. As part of this process, we do the following:

1. Fetch all resources into containerd.
2. Prepare the snapshot filesystem with the pulled resources.
3. Register metadata for the image.

ipfs daemon needs running.
`,
	Flags: append(append(commands.SnapshotterFlags, commands.LabelFlag),
		cli.StringSliceFlag{
			Name:  "platform",
			Usage: "Pull content from a specific platform",
			Value: &cli.StringSlice{},
		},
		cli.BoolFlag{
			Name:  "all-platforms",
			Usage: "pull content and metadata from all platforms",
		},
	),
	Action: func(clicontext *cli.Context) error {
		ref := clicontext.Args().First()
		if ref == "" {
			return fmt.Errorf("please provide CID to pull")
		}

		targetPlatforms := clicontext.StringSlice("platform") // len = 0 means all-platforms
		if !clicontext.Bool("all-platforms") && len(clicontext.StringSlice("platform")) == 0 {
			targetPlatforms = append(targetPlatforms, platforms.DefaultString())
		}

		client, ctx, cancel, err := commands.NewClient(clicontext)
		if err != nil {
			return err
		}
		defer cancel()

		ctx, done, err := client.WithLease(ctx)
		if err != nil {
			return err
		}
		defer done(ctx)

		ipfsClient, err := httpapi.NewLocalApi()
		if err != nil {
			return err
		}
		r, err := ipfs.NewResolver(ipfsClient, ipfs.ResolverOptions{
			Scheme: "ipfs", // TODO: make it configurable after deciding the image reference naming convention
		})
		if err != nil {
			return err
		}

		return pull(ctx, client, ref, pullConfig{
			resolver:        r,
			snapshotterName: clicontext.String("snapshotter"),
			targetPlatforms: targetPlatforms,
			progressOut:     !clicontext.GlobalBool("debug"),
			labels:          clicontext.StringSlice("label"),
		})
	},
}

type pullConfig struct {
	resolver        remotes.Resolver
	snapshotterName string
	targetPlatforms []string
	progressOut     bool
	labels          []string
}

func pull(ctx context.Context, client *containerd.Client, ref string, config pullConfig) error {
	ongoing := content.NewJobs(ref)
	pCtx, stopProgress := context.WithCancel(ctx)
	progress := make(chan struct{})

	go func() {
		if config.progressOut { // no progress bar if false, because it hides some debug logs
			content.ShowProgress(pCtx, ongoing, client.ContentStore(), os.Stdout)
		}
		close(progress)
	}()

	h := images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		if desc.MediaType != images.MediaTypeDockerSchema1Manifest {
			ongoing.Add(desc)
		}
		return nil, nil
	})

	rOpts := []containerd.RemoteOpt{
		containerd.WithPullLabels(commands.LabelArgs(config.labels)),
		containerd.WithResolver(config.resolver),
		containerd.WithImageHandler(h),
		containerd.WithSchema1Conversion,
		containerd.WithPullSnapshotter(config.snapshotterName),
	}

	if len(config.targetPlatforms) == 1 {
		// We can use Pull API only for pulling single platform
		_, err := client.Pull(pCtx, ref, append(rOpts, containerd.WithPullUnpack, containerd.WithPlatform(config.targetPlatforms[0]))...)
		stopProgress()
		if err != nil {
			return err
		}
		<-progress
		return nil
	}

	if len(config.targetPlatforms) == 0 { // all platforms
		rOpts = append(rOpts, containerd.WithAllMetadata())
	} else {
		for _, platform := range config.targetPlatforms {
			rOpts = append(rOpts, containerd.WithPlatform(platform))
		}
	}
	img, err := client.Fetch(pCtx, ref, rOpts...)
	stopProgress()
	if err != nil {
		return err
	}
	<-progress

	var unpackPlatforms []ocispec.Platform
	if len(config.targetPlatforms) == 0 { // all platforms
		unpackPlatforms, err = images.Platforms(ctx, client.ContentStore(), img.Target)
		if err != nil {
			return errors.Wrap(err, "unable to resolve image platforms")
		}
	} else {
		for _, s := range config.targetPlatforms {
			ps, err := platforms.Parse(s)
			if err != nil {
				return errors.Wrapf(err, "unable to parse platform %s", s)
			}
			unpackPlatforms = append(unpackPlatforms, ps)
		}
	}

	start := time.Now()
	for _, platform := range unpackPlatforms {
		fmt.Printf("unpacking %s %s...\n", platforms.Format(platform), img.Target.Digest)
		i := containerd.NewImageWithPlatform(client, img, platforms.Only(platform))
		if err := i.Unpack(ctx, config.snapshotterName); err != nil {
			return err
		}
	}
	fmt.Printf("done: %s\t\n", time.Since(start))

	return nil
}
