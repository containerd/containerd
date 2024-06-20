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

package snapshots

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/progress"
	"github.com/containerd/containerd/v2/pkg/rootfs"
	"github.com/containerd/log"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli/v2"
)

// Command is the cli command for managing snapshots
var Command = &cli.Command{
	Name:    "snapshots",
	Aliases: []string{"snapshot"},
	Usage:   "Manage snapshots",
	Flags:   commands.SnapshotterFlags,
	Subcommands: cli.Commands{
		commitCommand,
		diffCommand,
		infoCommand,
		listCommand,
		mountCommand,
		prepareCommand,
		removeCommand,
		setLabelCommand,
		treeCommand,
		unpackCommand,
		usageCommand,
		viewCommand,
	},
}

var listCommand = &cli.Command{
	Name:    "list",
	Aliases: []string{"ls"},
	Usage:   "List snapshots",
	Action: func(cliContext *cli.Context) error {
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()
		var (
			snapshotter = client.SnapshotService(cliContext.String("snapshotter"))
			tw          = tabwriter.NewWriter(os.Stdout, 1, 8, 1, ' ', 0)
		)
		fmt.Fprintln(tw, "KEY\tPARENT\tKIND\t")
		if err := snapshotter.Walk(ctx, func(ctx context.Context, info snapshots.Info) error {
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

var diffCommand = &cli.Command{
	Name:      "diff",
	Usage:     "Get the diff of two snapshots. the default second snapshot is the first snapshot's parent.",
	ArgsUsage: "[flags] <idA> [<idB>]",
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:  "media-type",
			Usage: "Media type to use for creating diff",
			Value: ocispec.MediaTypeImageLayerGzip,
		},
		&cli.StringFlag{
			Name:  "ref",
			Usage: "Content upload reference to use",
		},
		&cli.BoolFlag{
			Name:  "keep",
			Usage: "Keep diff content. up to creator to delete it.",
		},
	}, commands.LabelFlag),
	Action: func(cliContext *cli.Context) error {
		var (
			idA = cliContext.Args().First()
			idB = cliContext.Args().Get(1)
		)
		if idA == "" {
			return errors.New("snapshot id must be provided")
		}
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		ctx, done, err := client.WithLease(ctx)
		if err != nil {
			return err
		}
		defer done(ctx)

		var desc ocispec.Descriptor
		labels := commands.LabelArgs(cliContext.StringSlice("label"))
		snapshotter := client.SnapshotService(cliContext.String("snapshotter"))

		if cliContext.Bool("keep") {
			labels["containerd.io/gc.root"] = time.Now().UTC().Format(time.RFC3339)
		}
		opts := []diff.Opt{
			diff.WithMediaType(cliContext.String("media-type")),
			diff.WithReference(cliContext.String("ref")),
			diff.WithLabels(labels),
		}
		// SOURCE_DATE_EPOCH is propagated via the ctx, so no need to specify diff.WithSourceDateEpoch here

		if idB == "" {
			desc, err = rootfs.CreateDiff(ctx, idA, snapshotter, client.DiffService(), opts...)
			if err != nil {
				return err
			}
		} else {
			desc, err = withMounts(ctx, idA, snapshotter, func(a []mount.Mount) (ocispec.Descriptor, error) {
				return withMounts(ctx, idB, snapshotter, func(b []mount.Mount) (ocispec.Descriptor, error) {
					return client.DiffService().Compare(ctx, a, b, opts...)
				})
			})
			if err != nil {
				return err
			}
		}

		ra, err := client.ContentStore().ReaderAt(ctx, desc)
		if err != nil {
			return err
		}
		defer ra.Close()
		_, err = io.Copy(os.Stdout, content.NewReader(ra))

		return err
	},
}

func withMounts(ctx context.Context, id string, sn snapshots.Snapshotter, f func(mounts []mount.Mount) (ocispec.Descriptor, error)) (ocispec.Descriptor, error) {
	var mounts []mount.Mount
	info, err := sn.Stat(ctx, id)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if info.Kind == snapshots.KindActive {
		mounts, err = sn.Mounts(ctx, id)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
	} else {
		key := fmt.Sprintf("%s-view-key", id)
		mounts, err = sn.View(ctx, key, id)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		defer sn.Remove(ctx, key)
	}
	return f(mounts)
}

var usageCommand = &cli.Command{
	Name:      "usage",
	Usage:     "Usage snapshots",
	ArgsUsage: "[flags] [<key>, ...]",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "b",
			Usage: "Display size in bytes",
		},
	},
	Action: func(cliContext *cli.Context) error {
		var displaySize func(int64) string
		if cliContext.Bool("b") {
			displaySize = func(s int64) string {
				return strconv.FormatInt(s, 10)
			}
		} else {
			displaySize = func(s int64) string {
				return progress.Bytes(s).String()
			}
		}
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()
		var (
			snapshotter = client.SnapshotService(cliContext.String("snapshotter"))
			tw          = tabwriter.NewWriter(os.Stdout, 1, 8, 1, ' ', 0)
		)
		fmt.Fprintln(tw, "KEY\tSIZE\tINODES\t")
		if cliContext.NArg() == 0 {
			if err := snapshotter.Walk(ctx, func(ctx context.Context, info snapshots.Info) error {
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
			for _, id := range cliContext.Args().Slice() {
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

var removeCommand = &cli.Command{
	Name:      "delete",
	Aliases:   []string{"del", "remove", "rm"},
	ArgsUsage: "<key> [<key>, ...]",
	Usage:     "Remove snapshots",
	Action: func(cliContext *cli.Context) error {
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()
		snapshotter := client.SnapshotService(cliContext.String("snapshotter"))
		for _, key := range cliContext.Args().Slice() {
			err = snapshotter.Remove(ctx, key)
			if err != nil {
				return fmt.Errorf("failed to remove %q: %w", key, err)
			}
		}

		return nil
	},
}

var prepareCommand = &cli.Command{
	Name:      "prepare",
	Usage:     "Prepare a snapshot from a committed snapshot",
	ArgsUsage: "[flags] <key> [<parent>]",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "target",
			Aliases: []string{"t"},
			Usage:   "Mount target path, will print mount, if provided",
		},
		&cli.BoolFlag{
			Name:  "mounts",
			Usage: "Print out snapshot mounts as JSON",
		},
	},
	Action: func(cliContext *cli.Context) error {
		if narg := cliContext.NArg(); narg < 1 || narg > 2 {
			return cli.ShowSubcommandHelp(cliContext)
		}
		var (
			target = cliContext.String("target")
			key    = cliContext.Args().Get(0)
			parent = cliContext.Args().Get(1)
		)
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		snapshotter := client.SnapshotService(cliContext.String("snapshotter"))
		labels := map[string]string{
			"containerd.io/gc.root": time.Now().UTC().Format(time.RFC3339),
		}

		mounts, err := snapshotter.Prepare(ctx, key, parent, snapshots.WithLabels(labels))
		if err != nil {
			return err
		}

		if target != "" {
			printMounts(target, mounts)
		}

		if cliContext.Bool("mounts") {
			commands.PrintAsJSON(mounts)
		}

		return nil
	},
}

var viewCommand = &cli.Command{
	Name:      "view",
	Usage:     "Create a read-only snapshot from a committed snapshot",
	ArgsUsage: "[flags] <key> [<parent>]",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "target",
			Aliases: []string{"t"},
			Usage:   "Mount target path, will print mount, if provided",
		},
		&cli.BoolFlag{
			Name:  "mounts",
			Usage: "Print out snapshot mounts as JSON",
		},
	},
	Action: func(cliContext *cli.Context) error {
		if narg := cliContext.NArg(); narg < 1 || narg > 2 {
			return cli.ShowSubcommandHelp(cliContext)
		}
		var (
			target = cliContext.String("target")
			key    = cliContext.Args().Get(0)
			parent = cliContext.Args().Get(1)
		)
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		snapshotter := client.SnapshotService(cliContext.String("snapshotter"))
		mounts, err := snapshotter.View(ctx, key, parent)
		if err != nil {
			return err
		}

		if target != "" {
			printMounts(target, mounts)
		}

		if cliContext.Bool("mounts") {
			commands.PrintAsJSON(mounts)
		}

		return nil
	},
}

var mountCommand = &cli.Command{
	Name:      "mounts",
	Aliases:   []string{"m", "mount"},
	Usage:     "Mount gets mount commands for the snapshots",
	ArgsUsage: "<target> <key>",
	Action: func(cliContext *cli.Context) error {
		if cliContext.NArg() != 2 {
			return cli.ShowSubcommandHelp(cliContext)
		}
		var (
			target = cliContext.Args().Get(0)
			key    = cliContext.Args().Get(1)
		)
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()
		snapshotter := client.SnapshotService(cliContext.String("snapshotter"))
		mounts, err := snapshotter.Mounts(ctx, key)
		if err != nil {
			return err
		}

		printMounts(target, mounts)

		return nil
	},
}

var commitCommand = &cli.Command{
	Name:      "commit",
	Usage:     "Commit an active snapshot into the provided name",
	ArgsUsage: "<key> <active>",
	Action: func(cliContext *cli.Context) error {
		if cliContext.NArg() != 2 {
			return cli.ShowSubcommandHelp(cliContext)
		}
		var (
			key    = cliContext.Args().Get(0)
			active = cliContext.Args().Get(1)
		)
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()
		snapshotter := client.SnapshotService(cliContext.String("snapshotter"))
		labels := map[string]string{
			"containerd.io/gc.root": time.Now().UTC().Format(time.RFC3339),
		}
		return snapshotter.Commit(ctx, key, active, snapshots.WithLabels(labels))
	},
}

var treeCommand = &cli.Command{
	Name:  "tree",
	Usage: "Display tree view of snapshot branches",
	Action: func(cliContext *cli.Context) error {
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()
		var (
			snapshotter = client.SnapshotService(cliContext.String("snapshotter"))
			tree        = newSnapshotTree()
		)

		if err := snapshotter.Walk(ctx, func(ctx context.Context, info snapshots.Info) error {
			// Get or create node and add node details
			tree.add(info)
			return nil
		}); err != nil {
			return err
		}

		printTree(tree)

		return nil
	},
}

var infoCommand = &cli.Command{
	Name:      "info",
	Usage:     "Get info about a snapshot",
	ArgsUsage: "<key>",
	Action: func(cliContext *cli.Context) error {
		if cliContext.NArg() != 1 {
			return cli.ShowSubcommandHelp(cliContext)
		}

		key := cliContext.Args().Get(0)
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()
		snapshotter := client.SnapshotService(cliContext.String("snapshotter"))
		info, err := snapshotter.Stat(ctx, key)
		if err != nil {
			return err
		}

		commands.PrintAsJSON(info)

		return nil
	},
}

var setLabelCommand = &cli.Command{
	Name:        "label",
	Usage:       "Add labels to content",
	ArgsUsage:   "<name> [<label>=<value> ...]",
	Description: "labels snapshots in the snapshotter",
	Action: func(cliContext *cli.Context) error {
		key, labels := commands.ObjectWithLabelArgs(cliContext)
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		snapshotter := client.SnapshotService(cliContext.String("snapshotter"))

		info := snapshots.Info{
			Name:   key,
			Labels: map[string]string{},
		}

		var paths []string
		for k, v := range labels {
			paths = append(paths, fmt.Sprintf("labels.%s", k))
			if v != "" {
				info.Labels[k] = v
			}
		}

		// Nothing updated, do no clear
		if len(paths) == 0 {
			info, err = snapshotter.Stat(ctx, info.Name)
		} else {
			info, err = snapshotter.Update(ctx, info, paths...)
		}
		if err != nil {
			return err
		}

		var labelStrings []string
		for k, v := range info.Labels {
			labelStrings = append(labelStrings, fmt.Sprintf("%s=%s", k, v))
		}

		fmt.Println(strings.Join(labelStrings, ","))

		return nil
	},
}

var unpackCommand = &cli.Command{
	Name:      "unpack",
	Usage:     "Unpack applies layers from a manifest to a snapshot",
	ArgsUsage: "[flags] <digest>",
	Flags:     commands.SnapshotterFlags,
	Action: func(cliContext *cli.Context) error {
		dgst, err := digest.Parse(cliContext.Args().First())
		if err != nil {
			return err
		}
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()
		log.G(ctx).Debugf("unpacking layers from manifest %s", dgst.String())
		// TODO: Support unpack by name
		images, err := client.ListImages(ctx)
		if err != nil {
			return err
		}
		var unpacked bool
		for _, image := range images {
			if image.Target().Digest == dgst {
				fmt.Printf("unpacking %s (%s)...", dgst, image.Target().MediaType)
				if err := image.Unpack(ctx, cliContext.String("snapshotter")); err != nil {
					fmt.Println()
					return err
				}
				fmt.Println("done")
				unpacked = true
				break
			}
		}
		if !unpacked {
			return errors.New("manifest not found")
		}
		// TODO: Get rootfs from Image
		// log.G(ctx).Infof("chain ID: %s", chainID.String())
		return nil
	},
}

type snapshotTree struct {
	nodes []*snapshotTreeNode
	index map[string]*snapshotTreeNode
}

func newSnapshotTree() *snapshotTree {
	return &snapshotTree{
		index: make(map[string]*snapshotTreeNode),
	}
}

type snapshotTreeNode struct {
	info     snapshots.Info
	children []string
}

func (st *snapshotTree) add(info snapshots.Info) *snapshotTreeNode {
	entry, ok := st.index[info.Name]
	if !ok {
		entry = &snapshotTreeNode{info: info}
		st.nodes = append(st.nodes, entry)
		st.index[info.Name] = entry
	} else {
		entry.info = info // update info if we created placeholder
	}

	if info.Parent != "" {
		pn := st.get(info.Parent)
		if pn == nil {
			// create a placeholder
			pn = st.add(snapshots.Info{Name: info.Parent})
		}

		pn.children = append(pn.children, info.Name)
	}
	return entry
}

func (st *snapshotTree) get(name string) *snapshotTreeNode {
	return st.index[name]
}

func printTree(st *snapshotTree) {
	for _, node := range st.nodes {
		// Print for root(parent-less) nodes only
		if node.info.Parent == "" {
			printNode(node.info.Name, st, 0)
		}
	}
}

func printNode(name string, tree *snapshotTree, level int) {
	node := tree.index[name]
	prefix := strings.Repeat("  ", level)

	if level > 0 {
		prefix += "\\_"
	}

	fmt.Printf(prefix+" %s\n", node.info.Name)
	level++
	for _, child := range node.children {
		printNode(child, tree, level)
	}
}

func printMounts(target string, mounts []mount.Mount) {
	// FIXME: This is specific to Unix
	for _, m := range mounts {
		fmt.Printf("mount -t %s %s %s -o %s\n", m.Type, m.Source, filepath.Join(target, m.Target), strings.Join(m.Options, ","))
	}
}
