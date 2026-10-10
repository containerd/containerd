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
	"os"
	"slices"

	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	coreimages "github.com/containerd/containerd/v2/core/images"
	"github.com/urfave/cli/v2"
)

var repairCommand = &cli.Command{
	Name:      "repair",
	Usage:     "Verify, re-pull, and unpack a damaged image",
	ArgsUsage: "[flags] <ref>",
	Description: `Verify an image, remove its confirmed digest or size mismatches,
and re-pull it. Unreadable content is never removed automatically.`,
	Flags: append(slices.Clone(pullCommand.Flags),
		&cli.BoolFlag{
			Name:  "dry-run",
			Usage: "Report repair candidates without changing the image",
		},
	),
	Action: func(cliContext *cli.Context) error {
		ref := cliContext.Args().First()
		if ref == "" {
			return errors.New("please provide an image reference to repair")
		}

		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		image, err := client.GetImage(ctx, ref)
		if err != nil {
			return fmt.Errorf("failed to find image %q: %w", ref, err)
		}
		target := image.Target()
		matcher, manifestLimit, err := verificationMatcher(cliContext)
		if err != nil {
			return err
		}

		failures := verifyImage(ctx, client.ContentStore(), image.Target(), matcher, manifestLimit)
		if len(failures) == 0 {
			fmt.Fprintf(os.Stdout, "image %s is valid\n", ref)
			return nil
		}

		for _, failure := range failures {
			fmt.Fprintf(os.Stdout, "%s: %s\n", failure.desc.Digest, failure.err)
			if !failure.removable {
				return fmt.Errorf("cannot repair image %q: content %s is not safely removable", ref, failure.desc.Digest)
			}
		}
		if cliContext.Bool("dry-run") {
			return fmt.Errorf("image %q has %d repairable content mismatches", ref, len(failures))
		}

		for _, failure := range failures {
			if err := client.ContentStore().Delete(ctx, failure.desc.Digest); err != nil {
				return fmt.Errorf("failed to remove damaged content %s for %q: %w", failure.desc.Digest, ref, err)
			}
		}
		if err := client.ImageService().Delete(ctx, ref, coreimages.SynchronousDelete(), coreimages.DeleteTarget(&target)); err != nil {
			return fmt.Errorf("failed to remove damaged image %q: %w", ref, err)
		}
		if err := pullCommand.Action(cliContext); err != nil {
			return fmt.Errorf("failed to re-pull repaired image %q: %w", ref, err)
		}

		if err := client.Close(); err != nil {
			return fmt.Errorf("failed to close pre-repair client: %w", err)
		}
		client, ctx, cancel, err = commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()
		image, err = client.GetImage(ctx, ref)
		if err != nil {
			return fmt.Errorf("re-pulled image %q is not available: %w", ref, err)
		}
		if failures = verifyImage(ctx, client.ContentStore(), image.Target(), matcher, manifestLimit); len(failures) != 0 {
			return fmt.Errorf("repaired image %q still has %d invalid content descriptors", ref, len(failures))
		}
		fmt.Fprintf(os.Stdout, "image %s repaired successfully\n", ref)
		return nil
	},
}
