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
	"os"

	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/pkg/display"
	"github.com/urfave/cli/v2"
)

var inspectCommand = &cli.Command{
	Name:        "inspect",
	Aliases:     []string{"i"},
	Usage:       "inspect an image",
	ArgsUsage:   "<image> [flags]",
	Description: `Inspect an image`,
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "content",
			Usage: "Show JSON content",
		},
	},
	Action: func(clicontext *cli.Context) error {
		client, ctx, cancel, err := commands.NewClient(clicontext)
		if err != nil {
			return err
		}
		defer cancel()
		var (
			ref        = clicontext.Args().First()
			imageStore = client.ImageService()
			cs         = client.ContentStore()
		)

		img, err := imageStore.Get(ctx, ref)
		if err != nil {
			return err
		}

		opts := []display.PrintOpt{
			display.WithWriter(os.Stdout),
		}
		if clicontext.Bool("content") {
			opts = append(opts, display.Verbose)
		}

		return display.NewImageTreePrinter(opts...).PrintImageTree(ctx, img, cs)
	},
}
