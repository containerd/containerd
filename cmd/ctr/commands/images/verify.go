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
	"sort"
	"text/tabwriter"

	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/core/content"
	coreimages "github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli/v2"
)

var verifyCommand = &cli.Command{
	Name:      "verify",
	Usage:     "Verify image content against descriptor digests",
	ArgsUsage: "[<filter>, ...]",
	Description: `Verify all content referenced by matching images. With --delete,
blobs with a confirmed size or digest mismatch are removed after verification.`,
	Flags: []cli.Flag{
		&cli.StringSliceFlag{
			Name:  "platform",
			Usage: "Verify content for a specific platform",
			Value: cli.NewStringSlice(),
		},
		&cli.BoolFlag{
			Name:  "all-platforms",
			Usage: "Verify content for all platforms",
		},
		&cli.BoolFlag{
			Name:  "delete",
			Usage: "Delete blobs with a confirmed size or digest mismatch",
		},
	},
	Action: func(cliContext *cli.Context) error {
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		imageList, err := client.ListImages(ctx, cliContext.Args().Slice()...)
		if err != nil {
			return fmt.Errorf("failed listing images: %w", err)
		}

		contentStore := client.ContentStore()
		matcher, manifestLimit, err := verificationMatcher(cliContext)
		if err != nil {
			return err
		}
		failures := make(map[digest.Digest]verifyFailure)
		for _, image := range imageList {
			for _, failure := range verifyImage(ctx, contentStore, image.Target(), matcher, manifestLimit) {
				failures[failure.desc.Digest] = failure
			}
		}

		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, ' ', 0)
		defer tw.Flush()
		fmt.Fprintln(tw, "DIGEST\tSTATUS\tDETAIL")
		var digests []string
		for dgst := range failures {
			digests = append(digests, dgst.String())
		}
		sort.Strings(digests)

		unresolved := false
		for _, value := range digests {
			failure := failures[digest.Digest(value)]
			status := "error"
			if failure.removable && cliContext.Bool("delete") {
				if err := contentStore.Delete(ctx, failure.desc.Digest); err != nil {
					failure.err = fmt.Errorf("delete: %w", err)
					unresolved = true
				} else {
					status = "deleted"
				}
			} else if cliContext.Bool("delete") {
				unresolved = true
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", failure.desc.Digest, status, failure.err)
		}

		if len(failures) > 0 && !cliContext.Bool("delete") {
			return fmt.Errorf("image content verification found %d invalid or unreadable blobs", len(failures))
		}
		if unresolved {
			return fmt.Errorf("image content verification left one or more blobs unresolved")
		}
		return nil
	},
}

func verificationMatcher(cliContext *cli.Context) (platforms.MatchComparer, int, error) {
	values := cliContext.StringSlice("platform")
	if cliContext.Bool("all-platforms") {
		if len(values) > 0 {
			return nil, 0, errors.New("cannot specify both --platform and --all-platforms")
		}
		return nil, 0, nil
	}
	if len(values) > 0 {
		parsed, err := platforms.ParseAll(values)
		if err != nil {
			return nil, 0, err
		}
		manifestLimit := 0
		if len(parsed) == 1 {
			manifestLimit = 1
		}
		return platforms.Ordered(parsed...), manifestLimit, nil
	}
	return platforms.MatchComparer(platforms.Default()), 1, nil
}

type verifyFailure struct {
	desc      ocispec.Descriptor
	err       error
	removable bool
}

func verifyImage(ctx context.Context, provider content.Provider, target ocispec.Descriptor, matcher platforms.MatchComparer, manifestLimit int) []verifyFailure {
	pending := []ocispec.Descriptor{target}
	seen := make(map[digest.Digest]struct{})
	var failures []verifyFailure

	for len(pending) > 0 {
		desc := pending[0]
		pending = pending[1:]
		if _, ok := seen[desc.Digest]; ok {
			continue
		}
		seen[desc.Digest] = struct{}{}

		if err := coreimages.VerifyDescriptor(ctx, provider, desc); err != nil {
			failures = append(failures, verifyFailure{
				desc:      desc,
				err:       err,
				removable: errors.Is(err, coreimages.ErrContentMismatch),
			})
			continue
		}

		childrenHandler := coreimages.ChildrenHandler(provider)
		if matcher != nil {
			childrenHandler = coreimages.FilterPlatforms(childrenHandler, matcher)
			if manifestLimit > 0 {
				childrenHandler = coreimages.LimitManifests(childrenHandler, matcher, manifestLimit)
			}
		}
		children, err := childrenHandler(ctx, desc)
		if err != nil {
			failures = append(failures, verifyFailure{desc: desc, err: err})
			continue
		}
		pending = append(pending, children...)
	}

	return failures
}
