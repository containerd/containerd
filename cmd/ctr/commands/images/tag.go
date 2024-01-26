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

	"github.com/urfave/cli"

	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/pkg/transfer/image"
	"github.com/distribution/reference"
)

var tagCommand = cli.Command{
	Name:        "tag",
	Usage:       "Tag an image",
	ArgsUsage:   "[flags] <source_ref> <target_ref> [<target_ref>, ...]",
	Description: `Tag an image for use in containerd.`,
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "force",
			Usage: "Force target_ref to be created, regardless if it already exists",
		},
		cli.BoolTFlag{
			Name:  "local",
			Usage: "Run tag locally rather than through transfer API",
		},
		cli.BoolFlag{
			Name:  "skip-reference-check",
			Usage: "Skip the strict check for reference names",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			ref = context.Args().First()
		)
		if ref == "" {
			return fmt.Errorf("please provide an image reference to tag from")
		}
		if context.NArg() <= 1 {
			return fmt.Errorf("please provide an image reference to tag to")
		}

		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		if !context.BoolT("local") {
			for _, targetRef := range context.Args()[1:] {
				if !context.Bool("skip-reference-check") {
					if _, err := reference.ParseAnyReference(targetRef); err != nil {
						return fmt.Errorf("error parsing reference: %q is not a valid repository/tag %v", targetRef, err)
					}
				}
				err = client.Transfer(ctx, image.NewStore(ref), image.NewStore(targetRef))
				if err != nil {
					return err
				}
				fmt.Println(targetRef)
			}
			return nil
		}

		ctx, done, err := client.WithLease(ctx)
		if err != nil {
			return err
		}
		defer done(ctx)

		imageService := client.ImageService()
		image, err := imageService.Get(ctx, ref)
		if err != nil {
			return err
		}
		// Support multiple references for one command run
		for _, targetRef := range context.Args()[1:] {
			if !context.Bool("skip-reference-check") {
				if _, err := reference.ParseAnyReference(targetRef); err != nil {
					return fmt.Errorf("error parsing reference: %q is not a valid repository/tag %v", targetRef, err)
				}
			}
			image.Name = targetRef
			// Attempt to create the image first
			if _, err = imageService.Create(ctx, image); err != nil {
				// If user has specified force and the image already exists then
				// delete the original image and attempt to create the new one
				if errdefs.IsAlreadyExists(err) && context.Bool("force") {
					if err = imageService.Delete(ctx, targetRef); err != nil {
						return err
					}
					if _, err = imageService.Create(ctx, image); err != nil {
						return err
					}
				} else {
					return err
				}
			}
			fmt.Println(targetRef)
		}
		return nil
	},
}
