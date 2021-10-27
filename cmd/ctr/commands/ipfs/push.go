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
	"fmt"

	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/contrib/ipfs"
	"github.com/containerd/containerd/platforms"
	httpapi "github.com/ipfs/go-ipfs-http-client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

// Command is the cli command for interacting with IPFS.
var Command = cli.Command{
	Name:  "ipfs",
	Usage: "interact with IPFS",
	Subcommands: cli.Commands{
		pushCommand,
		pullCommand,
	},
}

var pushCommand = cli.Command{
	Name:      "push",
	Usage:     "push an image to a IPFS",
	ArgsUsage: "[flags] <image ref>",
	Description: `Pushes an image reference from containerd to IPFS.

	All resources associated with the manifest reference will be pushed.
	The ref is used to resolve to a locally existing image manifest.
	The image manifest must exist before push. During push, the image is
	converted to IPFS-enabled format.
	On success, IPFS CID of that image is printed.
	ipfs daemon needs running.
`,
	Flags: []cli.Flag{
		cli.StringSliceFlag{
			Name:  "platform",
			Usage: "Add content for a specific platform",
			Value: &cli.StringSlice{},
		},
		cli.BoolFlag{
			Name:  "all-platforms",
			Usage: "Add content for all platforms",
		},
	},
	Action: func(context *cli.Context) error {
		ref := context.Args().Get(0)
		if ref == "" {
			return errors.New("image need to be specified")
		}

		var platformMC platforms.MatchComparer
		if context.Bool("all-platforms") {
			platformMC = platforms.All
		} else {
			if pss := context.StringSlice("platform"); len(pss) > 0 {
				var all []ocispec.Platform
				for _, ps := range pss {
					p, err := platforms.Parse(ps)
					if err != nil {
						return errors.Wrapf(err, "invalid platform %q", ps)
					}
					all = append(all, p)
				}
				platformMC = platforms.Ordered(all...)
			} else {
				platformMC = platforms.DefaultStrict()
			}
		}

		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		ipfsClient, err := httpapi.NewLocalApi()
		if err != nil {
			return err
		}
		p, err := ipfs.Push(ctx, client, ipfsClient, ref, nil, platformMC)
		if err != nil {
			return err
		}
		fmt.Println(p.Cid().String())
		return nil
	},
}
