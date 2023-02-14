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
	"os"
	"text/tabwriter"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/pkg/progress"

	"github.com/opencontainers/image-spec/identity"
	"github.com/urfave/cli"
)

var usageCommand = cli.Command{
	Name:      "usage",
	Usage:     "Display usage of snapshots for a given image ref",
	ArgsUsage: "[flags] <ref>",
	Flags:     commands.SnapshotterFlags,
	Action: func(context *cli.Context) error {
		var ref = context.Args().First()
		if ref == "" {
			return fmt.Errorf("please provide an image reference to mount")
		}

		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		snapshotter := context.String("snapshotter")
		if snapshotter == "" {
			snapshotter = containerd.DefaultSnapshotter
		}

		img, err := client.ImageService().Get(ctx, ref)
		if err != nil {
			return fmt.Errorf("failed to ensure if image %s exists: %w", ref, err)
		}

		i := containerd.NewImage(client, img)
		if ok, err := i.IsUnpacked(ctx, snapshotter); err != nil {
			return fmt.Errorf("failed to ensure if image %s has been unpacked in snapshotter %s: %w",
				ref, snapshotter, err)
		} else if !ok {
			return fmt.Errorf("image %s isn't unpacked in snapshotter %s", ref, snapshotter)
		}

		diffIDs, err := i.RootFS(ctx)
		if err != nil {
			return err
		}

		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, ' ', 0)
		fmt.Fprintln(tw, "KEY\tSIZE\tINODES\t")

		snSrv := client.SnapshotService(snapshotter)
		snID := identity.ChainID(diffIDs).String()
		for snID != "" {
			usage, err := snSrv.Usage(ctx, snID)
			if err != nil {
				return fmt.Errorf("failed to get usage for snapshot %s: %w", snID, err)
			}

			fmt.Fprintf(tw, "%v\t%s\t%d\t\n",
				snID,
				progress.Bytes(usage.Size).String(),
				usage.Inodes,
			)

			info, err := snSrv.Stat(ctx, snID)
			if err != nil {
				return fmt.Errorf("failed to ensure if snapshot %s has parent or not: %w", snID, err)
			}
			snID = info.Parent
		}
		return tw.Flush()
	},
}
