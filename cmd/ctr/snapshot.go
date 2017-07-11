package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/containerd/progress"
	"github.com/containerd/containerd/rootfs"
	"github.com/containerd/containerd/snapshot"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var snapshotCommand = cli.Command{
	Name:  "snapshot",
	Usage: "snapshot management",
	Flags: snapshotterFlags,
	Subcommands: cli.Commands{
		archiveSnapshotCommand,
		listSnapshotCommand,
		usageSnapshotCommand,
		removeSnapshotCommand,
		prepareSnapshotCommand,
		treeSnapshotCommand,
		mountSnapshotCommand,
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

		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

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

		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

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

		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

		for _, id := range clicontext.Args() {
			err = snapshotter.Remove(ctx, id)
			if err != nil {
				return errors.Wrapf(err, "failed to remove %q", id)
			}
		}

		return nil
	},
}

var prepareSnapshotCommand = cli.Command{
	Name:      "prepare",
	Usage:     "prepare a snapshot from a committed snapshot",
	ArgsUsage: "[flags] digest target",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "snapshot-name",
			Usage: "name of the target snapshot",
		},
	},
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		if clicontext.NArg() != 2 {
			return cli.ShowSubcommandHelp(clicontext)
		}

		dgst, err := digest.Parse(clicontext.Args().Get(0))
		if err != nil {
			return err
		}

		target := clicontext.Args().Get(1)

		snapshotName := clicontext.String("snapshot-name")
		// Use the target as the snapshotName if no snapshot-name is provided
		if snapshotName == "" {
			snapshotName = target
		}

		logrus.Infof("preparing mounts %s", dgst.String())

		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

		mounts, err := snapshotter.Prepare(ctx, snapshotName, dgst.String())
		if err != nil {
			return err
		}

		for _, m := range mounts {
			fmt.Fprintf(os.Stdout, "mount -t %s %s %s -o %s\n", m.Type, m.Source, target, strings.Join(m.Options, ","))
		}

		return nil
	},
}

var mountSnapshotCommand = cli.Command{
	Name:      "mount",
	Usage:     "mount gets mount commands for the active snapshots",
	ArgsUsage: "[flags] target",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "snapshot-name",
			Usage: "name of the snapshot",
		},
	},
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		if clicontext.NArg() != 1 {
			return cli.ShowSubcommandHelp(clicontext)
		}

		target := clicontext.Args().Get(0)

		snapshotName := clicontext.String("snapshot-name")
		// Use the target as the snapshotName if no snapshot-name is provided
		if snapshotName == "" {
			snapshotName = target
		}

		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

		mounts, err := snapshotter.Mounts(ctx, snapshotName)
		if err != nil {
			return err
		}

		for _, m := range mounts {
			fmt.Fprintf(os.Stdout, "mount -t %s %s %s -o %s\n", m.Type, m.Source, target, strings.Join(m.Options, ","))
		}

		return nil
	},
}

var treeSnapshotCommand = cli.Command{
	Name:  "tree",
	Usage: "Display tree view of snapshot branches",
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

		tree := make(map[string]*snapshotTreeNode)

		if err := snapshotter.Walk(ctx, func(ctx context.Context, info snapshot.Info) error {
			// Get or create node and add node details
			node := getOrCreateTreeNode(info.Name, tree)
			if info.Parent != "" {
				node.Parent = info.Parent
				p := getOrCreateTreeNode(info.Parent, tree)
				p.Children = append(p.Children, info.Name)
			}

			return nil
		}); err != nil {
			return err
		}

		printTree(tree)

		return nil
	},
}

type snapshotTreeNode struct {
	Name     string
	Parent   string
	Children []string
}

func getOrCreateTreeNode(name string, tree map[string]*snapshotTreeNode) *snapshotTreeNode {
	if node, ok := tree[name]; ok {
		return node
	}
	node := &snapshotTreeNode{
		Name: name,
	}
	tree[name] = node
	return node
}

func printTree(tree map[string]*snapshotTreeNode) {
	for _, node := range tree {
		// Print for root(parent-less) nodes only
		if node.Parent == "" {
			printNode(node.Name, tree, 0)
		}
	}
}

func printNode(name string, tree map[string]*snapshotTreeNode, level int) {
	node := tree[name]
	fmt.Printf("%s\\_ %s\n", strings.Repeat("  ", level), node.Name)
	level++
	for _, child := range node.Children {
		printNode(child, tree, level)
	}
}
