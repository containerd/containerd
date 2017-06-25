package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/containerd/containerd/progress"
	"github.com/containerd/containerd/rootfs"
	"github.com/containerd/containerd/snapshot"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var snapshotCommand = cli.Command{
	Name:  "snapshot",
	Usage: "snapshot management",
	Subcommands: cli.Commands{
		archiveSnapshotCommand,
		listSnapshotCommand,
		usageSnapshotCommand,
		removeSnapshotCommand,
	},
}

var archiveSnapshotCommand = cli.Command{
	Name:      "archive",
	Usage:     "Create an archive of a snapshot",
	ArgsUsage: "[flags] id",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "id",
			Usage: "id of the container or snapshot",
		},
	},
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		id := clicontext.String("id")
		if id == "" {
			return errors.New("container id must be provided")
		}

		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

		differ, err := getDiffService(clicontext)
		if err != nil {
			return err
		}

		contentRef := fmt.Sprintf("diff-%s", id)

		d, err := rootfs.Diff(ctx, id, contentRef, snapshotter, differ)
		if err != nil {
			return err
		}

		// TODO: Track progress
		fmt.Printf("%s %s\n", d.MediaType, d.Digest)

		return nil
	},
}

var listSnapshotCommand = cli.Command{
	Name:    "list",
	Aliases: []string{"ls"},
	Usage:   "List snapshots",
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		client, err := newClient(clicontext)
		if err != nil {
			return err
		}

		snapshotter := client.SnapshotService()

		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, ' ', 0)
		fmt.Fprintln(tw, "ID\tParent\tState\tReadonly\t")

		if err := snapshotter.Walk(ctx, func(ctx context.Context, info snapshot.Info) error {
			fmt.Fprintf(tw, "%v\t%v\t%v\t%t\t\n", info.Name, info.Parent, state(info.Kind), info.Readonly)
			return nil
		}); err != nil {
			return err
		}

		return tw.Flush()
	},
}

func state(k snapshot.Kind) string {
	switch k {
	case snapshot.KindActive:
		return "active"
	case snapshot.KindCommitted:
		return "committed"
	default:
		return ""
	}
}

var usageSnapshotCommand = cli.Command{
	Name:      "usage",
	Usage:     "Usage snapshots",
	ArgsUsage: "[flags] [id] ...",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "b",
			Usage: "display size in bytes",
		},
	},
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		client, err := newClient(clicontext)
		if err != nil {
			return err
		}

		var displaySize func(int64) string
		if clicontext.Bool("b") {
			displaySize = func(s int64) string {
				return fmt.Sprintf("%d", s)
			}
		} else {
			displaySize = func(s int64) string {
				return progress.Bytes(s).String()
			}
		}

		snapshotter := client.SnapshotService()

		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, ' ', 0)
		fmt.Fprintln(tw, "ID\tSize\tInodes\t")

		if clicontext.NArg() == 0 {
			if err := snapshotter.Walk(ctx, func(ctx context.Context, info snapshot.Info) error {
				usage, err := snapshotter.Usage(ctx, info.Name)
				if err != nil {
					return err
				}
				fmt.Fprintf(tw, "%v\t%s\t%d\t\n", info.Name, displaySize(usage.Size), usage.Inodes)
				return nil
			}); err != nil {
				return err
			}
		} else {
			for _, id := range clicontext.Args() {
				usage, err := snapshotter.Usage(ctx, id)
				if err != nil {
					return err
				}
				fmt.Fprintf(tw, "%v\t%s\t%d\t\n", id, displaySize(usage.Size), usage.Inodes)
			}
		}

		return tw.Flush()
	},
}

var removeSnapshotCommand = cli.Command{
	Name:      "remove",
	Aliases:   []string{"rm"},
	ArgsUsage: "id [id] ...",
	Usage:     "remove snapshots",
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		client, err := newClient(clicontext)
		if err != nil {
			return err
		}

		snapshotter := client.SnapshotService()

		for _, id := range clicontext.Args() {
			err = snapshotter.Remove(ctx, id)
			if err != nil {
				return errors.Wrapf(err, "failed to remove %q", id)
			}
		}

		return nil
	},
}
