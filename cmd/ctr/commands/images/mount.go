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
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/opencontainers/image-spec/identity"
	"github.com/urfave/cli/v2"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/defaults"
)

var mountCommand = &cli.Command{
	Name:      "mount",
	Usage:     "Mount an image to a target path",
	ArgsUsage: "[flags] <ref> [<target>]",
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
		&cli.BoolFlag{
			Name:  "sync-fs",
			Usage: "Synchronize the underlying filesystem containing files when unpack images, false by default",
		},
		&cli.DurationFlag{
			Name:    "expiration",
			Aliases: []string{"x"},
			Usage:   "Set the expiration time for the mount and snapshots",
			Value:   1 * time.Hour,
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

		var key string
		if target == "" {
			t := time.Now()
			var b [3]byte
			rand.Read(b[:])
			key = fmt.Sprintf("ctr-images-mount-%d-%s", t.Nanosecond(), base64.URLEncoding.EncodeToString(b[:]))
		} else {
			key = target
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
			leases.WithID(key),
			leases.WithExpiration(cliContext.Duration("expiration")),
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
		if err := i.Unpack(ctx, snapshotter, containerd.WithUnpackApplyOpts(diff.WithSyncFs(cliContext.Bool("sync-fs")))); err != nil {
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
			mounts, err = s.Prepare(ctx, key, chainID)
		} else {
			mounts, err = s.View(ctx, key, chainID)
		}
		if err != nil {
			if errdefs.IsAlreadyExists(err) {
				mounts, err = s.Mounts(ctx, key)
			}
			if err != nil {
				return err
			}
		}

		mm := client.MountManager()

		info, err := mm.Activate(ctx, key, mounts, mount.WithTemporary)
		if err == nil {
			mounts = info.System
		} else if !errdefs.IsNotImplemented(err) {
			return fmt.Errorf("activate error: %w", err)
		}

		if target != "" {
			if err := mount.All(mounts, target); err != nil {
				if err := s.Remove(ctx, key); err != nil && !errdefs.IsNotFound(err) {
					fmt.Fprintln(cliContext.App.ErrWriter, "Error cleaning up snapshot after mount error:", err)
				}
				return fmt.Errorf("failed to mount %v: %w", mounts, err)
			}
		} else if len(mounts) == 1 && mounts[0].Type == "bind" {
			target = mounts[0].Source
		} else {
			return fmt.Errorf("cannot handle returned mounts: %v", mounts)
		}

		fmt.Fprintln(cliContext.App.Writer, target)
		return nil
	},
}
