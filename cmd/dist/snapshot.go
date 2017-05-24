package main

import (
	contextpkg "context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshot"
	"github.com/containerd/containerd/snapshot/overlay"
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
		snapshotListCommand,
		snapshotPrepareCommand,
		snapshotMountCommand,
		snapshotViewCommand,
		snapshotCommitCommand,
		// snapRollbackCommand,
		snapshotStatCommand,
		snapshotRemoveCommand,
	},
}

var snapshotPrepareCommand = cli.Command{
	Name:        "prepare",
	Usage:       "prepare a snapshot",
	ArgsUsage:   "<key> [<parent>]",
	Description: `Prepares an Active Snapshot layer [Read/Write]`,
	Action: func(context *cli.Context) error {
		var (
			root   = context.GlobalString("root")
			key    = context.Args().First()
			parent = context.Args().Get(1)
		)

		root, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		log.G(contextpkg.Background()).WithField("root", root).Debug("setting up snapshot driver")

		//TODO: Should be configurable to other snapshot drivers.
		//hint: btrfs.NewSnapshotter(device, root)
		driver, err := overlay.NewSnapshotter(root)
		if err != nil {
			return err
		}

		if key == "" {
			cli.ShowSubcommandHelp(context)
			return fmt.Errorf("key required")
		}

		mounts, err := driver.Prepare(contextpkg.Background(), key, parent)
		if err != nil {
			return err
		}

		for _, mount := range mounts {
			fmt.Println(mount)
		}

		return nil
	},
}

var snapshotViewCommand = cli.Command{
	Name:        "view",
	Usage:       "Create a view of snapshot",
	ArgsUsage:   "<key> [<parent>]",
	Description: `Creates an View of Snapshot layer [Read Only]`,
	Action: func(context *cli.Context) error {
		var (
			root   = context.GlobalString("root")
			key    = context.Args().First()
			parent = context.Args().Get(1)
		)

		root, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		log.G(contextpkg.Background()).WithField("root", root).Debug("setting up snapshot driver")

		driver, err := overlay.NewSnapshotter(root)
		if err != nil {
			return err
		}

		if key == "" {
			cli.ShowSubcommandHelp(context)
			return fmt.Errorf("key required")
		}

		mounts, err := driver.View(contextpkg.Background(), key, parent)
		if err != nil {
			return err
		}

		for _, mount := range mounts {
			fmt.Println(mount)
		}

		return nil
	},
}

var snapshotRemoveCommand = cli.Command{
	Name:      "remove",
	Usage:     "Remove a snapshot",
	ArgsUsage: "<key> ",
	Action: func(context *cli.Context) error {
		var (
			root = context.GlobalString("root")
			key  = context.Args().First()
		)

		root, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		log.G(contextpkg.Background()).WithField("root", root).Debug("setting up snapshot driver")

		driver, err := overlay.NewSnapshotter(root)
		if err != nil {
			return err
		}

		if key == "" {
			cli.ShowSubcommandHelp(context)
			return fmt.Errorf("key required")
		}

		err = driver.Remove(contextpkg.Background(), key)
		if err != nil {
			return err
		}

		return nil
	},
}

var snapshotCommitCommand = cli.Command{
	Name:        "commit",
	Usage:       "commit an active snapshot",
	ArgsUsage:   "<name> <key>",
	Description: `Commit the Active layer with name provided`,
	Action: func(context *cli.Context) error {
		var (
			root = context.GlobalString("root")
			name = context.Args().First()
			key  = context.Args().Get(1)
		)

		root, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		log.G(contextpkg.Background()).WithField("root", root).Debug("setting up snapshot driver")

		driver, err := overlay.NewSnapshotter(root)
		if err != nil {
			return err
		}

		if name == "" {
			cli.ShowSubcommandHelp(context)
			return fmt.Errorf("name required")
		}

		if key == "" {
			cli.ShowSubcommandHelp(context)
			return fmt.Errorf("key required")
		}

		return driver.Commit(contextpkg.Background(), name, key)
	},
}

var snapshotMountCommand = cli.Command{
	Name:        "mount",
	Usage:       "mount an active snapshot",
	ArgsUsage:   "<dest> <key> [<parent>]",
	Description: `Mount active snapshot to destination folder.`,
	Action: func(context *cli.Context) error {
		var (
			root   = context.GlobalString("root")
			dst    = context.Args().First()
			key    = context.Args().Get(1)
			parent = context.Args().Get(2)
		)

		root, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		log.G(contextpkg.Background()).WithField("root", root).Debug("setting up snapshot driver")

		driver, err := overlay.NewSnapshotter(root)
		if err != nil {
			return err
		}

		if dst == "" {
			cli.ShowSubcommandHelp(context)
			return fmt.Errorf("dst required")
		}

		if key == "" {
			return fmt.Errorf("key required")
		}

		log.G(contextpkg.Background()).WithField("key", key).WithField("parent", parent).Info("preparing snapshot")

		mounts, err := driver.Mounts(contextpkg.Background(), key)
		if err != nil {
			return err
		}

		log.G(contextpkg.Background()).WithField("dst", dst).Info("mounting prepared snapshot")
		if err := containerd.MountAll(mounts, dst); err != nil {
			log.G(contextpkg.Background()).WithError(err).Error("error mounting")
			return err
		}
		return nil
	},
}

var snapshotStatCommand = cli.Command{
	Name:      "stat",
	Usage:     "Gives info of snapshot layer",
	ArgsUsage: "<key> ",
	Action: func(context *cli.Context) error {
		var (
			root = context.GlobalString("root")
			key  = context.Args().First()
		)

		root, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		log.G(contextpkg.Background()).WithField("root", root).Debug("setting up snapshot driver")

		driver, err := overlay.NewSnapshotter(root)
		if err != nil {
			return err
		}

		if key == "" {
			cli.ShowSubcommandHelp(context)
			return fmt.Errorf("key required")
		}

		info, err := driver.Stat(contextpkg.Background(), key)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
		defer tw.Flush()
		fmt.Fprintln(tw, "NAME\tTYPE\tREADONLY\tPARENT")
		fmt.Fprintf(tw, "%s\t%v\t%t\t%s\n", info.Name, info.Kind, info.Readonly, info.Parent)

		return nil
	},
}

type walkFunc func(contextpkg.Context, snapshot.Info) error

var snapshotListCommand = cli.Command{
	Name:  "list",
	Usage: "List all snapshot for root",
	Action: func(context *cli.Context) error {
		var (
			root = context.GlobalString("root")
		)

		root, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		log.G(contextpkg.Background()).WithField("root", root).Debug("setting up snapshot driver")

		driver, err := overlay.NewSnapshotter(root)
		if err != nil {
			return err
		}
		//var walkFn walkFunc
		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
		defer tw.Flush()
		fmt.Fprintln(tw, "NAME\tTYPE\tREADONLY\tPARENT")

		walkFn := func(ctx contextpkg.Context, in snapshot.Info) error {

			fmt.Fprintf(tw, "%s\t%v\t%t\t%s\n", in.Name, in.Kind, in.Readonly, in.Parent)
			return nil
		}

		return driver.Walk(contextpkg.Background(), walkFn)
	},
}
