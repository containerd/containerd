package main

import (
	contextpkg "context"
	"fmt"
	"path/filepath"

	"github.com/docker/containerd"
	"github.com/docker/containerd/log"
	"github.com/docker/containerd/snapshot/overlay"
	"github.com/urfave/cli"
)

var snapshotCommand = cli.Command{
	Name:  "snapshot",
	Usage: "manage snapshots",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "root",
			Usage: "root directory for snapshots",
			Value: ".snapshots",
		},
	},
	Subcommands: cli.Commands{
		// snapListCommand,
		// snapshotActiveCommand,
		snapshotPrepareCommand,
		snapshotMountCommand,
		// snapViewCommand,
		snapshotCommitCommand,
		// snapRollbackCommand,
	},
}

var snapshotPrepareCommand = cli.Command{
	Name:  "prepare",
	Usage: "prepare a snapshot",
	Action: func(context *cli.Context) error {
		var (
			ctx    = contextpkg.Background()
			root   = context.GlobalString("root")
			key    = context.Args().First()
			parent = context.Args().Get(1)
		)

		root, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		log.G(ctx).WithField("root", root).Debug("setting up snapshot driver")

		driver, err := overlay.NewOverlay(root)
		if err != nil {
			return err
		}

		if key == "" {
			return fmt.Errorf("key required")
		}

		mounts, err := driver.Prepare(key, parent)
		if err != nil {
			return err
		}

		for _, mount := range mounts {
			fmt.Println(mount)
		}

		return nil
	},
}

var snapshotCommitCommand = cli.Command{
	Name:  "commit",
	Usage: "commit an active snapshot",
	Action: func(context *cli.Context) error {
		var (
			ctx  = contextpkg.Background()
			root = context.GlobalString("root")
			name = context.Args().First()
			key  = context.Args().Get(1)
		)

		root, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		log.G(ctx).WithField("root", root).Debug("setting up snapshot driver")

		driver, err := overlay.NewOverlay(root)
		if err != nil {
			return err
		}

		if name == "" {
			return fmt.Errorf("name required")
		}

		if key == "" {
			return fmt.Errorf("key required")
		}

		return driver.Commit(name, key)
	},
}

var snapshotMountCommand = cli.Command{
	Name:  "mount",
	Usage: "mount an active snapshot",
	Action: func(context *cli.Context) error {
		var (
			ctx    = contextpkg.Background()
			root   = context.GlobalString("root")
			dst    = context.Args().First()
			key    = context.Args().Get(1)
			parent = context.Args().Get(2)
		)

		root, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		log.G(ctx).WithField("root", root).Debug("setting up snapshot driver")

		driver, err := overlay.NewOverlay(root)
		if err != nil {
			return err
		}

		if dst == "" {
			return fmt.Errorf("dst required")
		}

		if key == "" {
			return fmt.Errorf("key required")
		}

		log.G(ctx).WithField("key", key).WithField("parent", parent).Info("preparing snapshot")
		// TODO(stevvooe): This should query another method to get the mounts
		// for an active snapshot. For now, overlay driver is buggy and allows
		// calling prepare multiple times.
		mounts, err := driver.Prepare(key, parent)
		if err != nil {
			return err
		}

		log.G(ctx).WithField("dst", dst).Info("mounting prepared snapshot")
		if err := containerd.MountAll(mounts, dst); err != nil {
			log.G(ctx).WithError(err).Error("error mounting")
			return err
		}

		return nil
	},
}
