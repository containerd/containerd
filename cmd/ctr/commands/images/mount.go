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
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/opencontainers/image-spec/identity"
	"github.com/urfave/cli/v2"
)

var mountCommand = &cli.Command{
	Name:      "mount",
	Usage:     "Mount an image to a target path",
	ArgsUsage: "[flags] <ref> <target>",
	Description: `Mount an image rootfs to a specified path.

When you are done, use the unmount command.
`,
	Flags: append(append(commands.RegistryFlags, append(commands.SnapshotterFlags, commands.LabelFlag)...),
		&cli.BoolFlag{
			Name:  "rw",
			Usage: "Enable write support on the mount",
		},
		&cli.StringFlag{
			Name:  "platform",
			Usage: "Mount the image for the specified platform",
			Value: platforms.DefaultString(),
		},
	),
	Action: func(cliContext *cli.Context) (retErr error) {
		var (
			ref    = cliContext.Args().First()
			target = cliContext.Args().Get(1)
		)
		if ref == "" {
			return errors.New("please provide an image reference to mount")
		}
		if target == "" {
			return errors.New("please provide a target path to mount to")
		}

		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		snapshotter := cliContext.String("snapshotter")
		if snapshotter == "" {
			snapshotter = defaults.DefaultSnapshotter
		}

		ctx, done, err := client.WithLease(ctx,
			leases.WithID(target),
			leases.WithExpiration(24*time.Hour),
			leases.WithLabel("containerd.io/gc.ref.snapshot."+snapshotter, target),
		)
		if err != nil && !errdefs.IsAlreadyExists(err) {
			return err
		}

		defer func() {
			if retErr != nil && done != nil {
				done(ctx)
			}
		}()

		ps := cliContext.String("platform")
		p, err := platforms.Parse(ps)
		if err != nil {
			return fmt.Errorf("unable to parse platform %s: %w", ps, err)
		}

		img, err := client.ImageService().Get(ctx, ref)
		if err != nil {
			return err
		}

		i := containerd.NewImageWithPlatform(client, img, platforms.Only(p))
		if err := i.Unpack(ctx, snapshotter); err != nil {
			return fmt.Errorf("error unpacking image: %w", err)
		}

		diffIDs, err := i.RootFS(ctx)
		if err != nil {
			return err
		}
		chainID := identity.ChainID(diffIDs).String()
		fmt.Println(chainID)

		s := client.SnapshotService(snapshotter)

		var mounts []mount.Mount
		if cliContext.Bool("rw") {
			mounts, err = s.Prepare(ctx, target, chainID)
		} else {
			mounts, err = s.View(ctx, target, chainID)
		}
		if err != nil {
			if errdefs.IsAlreadyExists(err) {
				mounts, err = s.Mounts(ctx, target)
			}
			if err != nil {
				return err
			}
		}

		if err := mount.All(mounts, target); err != nil {
			if err := s.Remove(ctx, target); err != nil && !errdefs.IsNotFound(err) {
				fmt.Fprintln(cliContext.App.ErrWriter, "Error cleaning up snapshot after mount error:", err)
			}
			return err
		}

		fmt.Fprintln(cliContext.App.Writer, target)
		return nil
	},
}
