package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/progress"
	"github.com/containerd/containerd/snapshot"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var snapshotCommand = cli.Command{
	Name:  "snapshot",
	Usage: "snapshot management",
	Flags: snapshotterFlags,
	Subcommands: cli.Commands{
		listSnapshotCommand,
		usageSnapshotCommand,
		removeSnapshotCommand,
		prepareSnapshotCommand,
		viewSnapshotCommand,
		treeSnapshotCommand,
		mountSnapshotCommand,
		commitSnapshotCommand,
		infoSnapshotCommand,
	},
}

var listSnapshotCommand = cli.Command{
	Name:    "list",
	Aliases: []string{"ls"},
	Usage:   "list snapshots",
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, ' ', 0)
		fmt.Fprintln(tw, "KEY\tPARENT\tKIND\t")

		if err := snapshotter.Walk(ctx, func(ctx context.Context, info snapshot.Info) error {
			fmt.Fprintf(tw, "%v\t%v\t%v\t\n",
				info.Name,
				info.Parent,
				info.Kind)
			return nil
		}); err != nil {
			return err
		}

		return tw.Flush()
	},
}

var usageSnapshotCommand = cli.Command{
	Name:      "usage",
	Usage:     "usage snapshots",
	ArgsUsage: "[flags] [<key>, ...]",
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
		fmt.Fprintln(tw, "KEY\tSIZE\tINODES\t")

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
	ArgsUsage: "<key> [<key>, ...]",
	Usage:     "remove snapshots",
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

		for _, key := range clicontext.Args() {
			err = snapshotter.Remove(ctx, key)
			if err != nil {
				return errors.Wrapf(err, "failed to remove %q", key)
			}
		}

		return nil
	},
}

var prepareSnapshotCommand = cli.Command{
	Name:      "prepare",
	Usage:     "prepare a snapshot from a committed snapshot",
	ArgsUsage: "[flags] <key> [<parent>]",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "target, t",
			Usage: "mount target path, will print mount, if provided",
		},
	},
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		if clicontext.NArg() != 2 {
			return cli.ShowSubcommandHelp(clicontext)
		}

		target := clicontext.String("target")
		key := clicontext.Args().Get(0)
		parent := clicontext.Args().Get(1)

		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

		mounts, err := snapshotter.Prepare(ctx, key, parent)
		if err != nil {
			return err
		}

		if target != "" {
			printMounts(target, mounts)
		}

		return nil
	},
}

var viewSnapshotCommand = cli.Command{
	Name:      "view",
	Usage:     "create a read-only snapshot from a committed snapshot",
	ArgsUsage: "[flags] <key> [<parent>]",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "target, t",
			Usage: "mount target path, will print mount, if provided",
		},
	},
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		if clicontext.NArg() != 2 {
			return cli.ShowSubcommandHelp(clicontext)
		}

		target := clicontext.String("target")
		key := clicontext.Args().Get(0)
		parent := clicontext.Args().Get(1)

		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

		mounts, err := snapshotter.View(ctx, key, parent)
		if err != nil {
			return err
		}

		if target != "" {
			printMounts(target, mounts)
		}

		return nil
	},
}

var mountSnapshotCommand = cli.Command{
	Name:      "mounts",
	Aliases:   []string{"m", "mount"},
	Usage:     "mount gets mount commands for the snapshots",
	ArgsUsage: "[flags] <target> <key>",
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		if clicontext.NArg() != 2 {
			return cli.ShowSubcommandHelp(clicontext)
		}

		target := clicontext.Args().Get(0)
		key := clicontext.Args().Get(1)
		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

		mounts, err := snapshotter.Mounts(ctx, key)
		if err != nil {
			return err
		}

		printMounts(target, mounts)

		return nil
	},
}

var commitSnapshotCommand = cli.Command{
	Name:      "commit",
	Usage:     "commit an active snapshot into the provided name",
	ArgsUsage: "[flags] <key> <active>",
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		if clicontext.NArg() != 2 {
			return cli.ShowSubcommandHelp(clicontext)
		}

		key := clicontext.Args().Get(0)
		active := clicontext.Args().Get(1)

		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

		return snapshotter.Commit(ctx, key, active)
	},
}

var treeSnapshotCommand = cli.Command{
	Name:  "tree",
	Usage: "display tree view of snapshot branches",
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

var infoSnapshotCommand = cli.Command{
	Name:      "info",
	Usage:     "get info about a snapshot",
	ArgsUsage: "<key>",
	Action: func(clicontext *cli.Context) error {
		ctx, cancel := appContext(clicontext)
		defer cancel()

		if clicontext.NArg() != 1 {
			return cli.ShowSubcommandHelp(clicontext)
		}

		key := clicontext.Args().Get(0)

		snapshotter, err := getSnapshotter(clicontext)
		if err != nil {
			return err
		}

		info, err := snapshotter.Stat(ctx, key)
		if err != nil {
			return err
		}

		printAsJSON(info)

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

func printMounts(target string, mounts []mount.Mount) {
	// FIXME: This is specific to Unix
	for _, m := range mounts {
		fmt.Printf("mount -t %s %s %s -o %s\n", m.Type, m.Source, target, strings.Join(m.Options, ","))
	}
}
