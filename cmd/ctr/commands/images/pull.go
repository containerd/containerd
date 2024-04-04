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
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/transfer"
	"github.com/containerd/containerd/v2/core/transfer/image"
	"github.com/containerd/containerd/v2/core/transfer/registry"
	"github.com/containerd/containerd/v2/pkg/progress"
	"github.com/containerd/log"
	"github.com/containerd/platforms"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli/v2"
)

var pullCommand = &cli.Command{
	Name:      "pull",
	Usage:     "Pull an image from a remote",
	ArgsUsage: "[flags] <ref>",
	Description: `Fetch and prepare an image for use in containerd.

After pulling an image, it should be ready to use the same reference in a run
command. As part of this process, we do the following:

1. Fetch all resources into containerd.
2. Prepare the snapshot filesystem with the pulled resources.
3. Register metadata for the image.
`,
	Flags: append(append(commands.RegistryFlags, append(commands.SnapshotterFlags, commands.LabelFlag)...),
		&cli.StringSliceFlag{
			Name:  "platform",
			Usage: "Pull content from a specific platform",
			Value: cli.NewStringSlice(),
		},
		&cli.BoolFlag{
			Name:  "all-platforms",
			Usage: "Pull content and metadata from all platforms",
		},
		&cli.BoolFlag{
			Name:   "all-metadata",
			Usage:  "(Deprecated: use skip-metadata) Pull metadata for all platforms",
			Hidden: true,
		},
		&cli.BoolFlag{
			Name:  "skip-metadata",
			Usage: "Skips metadata for unused platforms (Image may be unable to be pushed without metadata)",
		},
		&cli.BoolFlag{
			Name:  "print-chainid",
			Usage: "Print the resulting image's chain ID",
		},
		&cli.IntFlag{
			Name:  "max-concurrent-downloads",
			Usage: "Set the max concurrent downloads for each pull",
		},
		&cli.BoolFlag{
			Name:  "local",
			Usage: "Fetch content from local client rather than using transfer service",
		},
	),
	Action: func(context *cli.Context) error {
		var (
			ref = context.Args().First()
		)
		if ref == "" {
			return fmt.Errorf("please provide an image reference to pull")
		}

		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		if !context.Bool("local") {
			ch, err := commands.NewStaticCredentials(ctx, context, ref)
			if err != nil {
				return err
			}

			var sopts []image.StoreOpt
			if !context.Bool("all-platforms") {
				p, err := platforms.ParseAll(context.StringSlice("platform"))
				if err != nil {
					return err
				}
				if len(p) == 0 {
					p = append(p, platforms.DefaultSpec())
				}
				sopts = append(sopts, image.WithPlatforms(p...))

				// Set unpack configuration
				for _, platform := range p {
					sopts = append(sopts, image.WithUnpack(platform, context.String("snapshotter")))
				}
			}
			// TODO: Support unpack for all platforms..?
			// Pass in a *?

			if context.Bool("metadata-only") {
				sopts = append(sopts, image.WithAllMetadata)
				// Any with an empty set is None
				// TODO: Specify way to specify not default platform
				// config.PlatformMatcher = platforms.Any()
			} else if !context.Bool("skip-metadata") {
				sopts = append(sopts, image.WithAllMetadata)
			}

			opts := []registry.Opt{registry.WithCredentials(ch), registry.WithHostDir(context.String("hosts-dir"))}
			if context.Bool("plain-http") {
				opts = append(opts, registry.WithDefaultScheme("http"))
			}
			reg, err := registry.NewOCIRegistry(ctx, ref, opts...)
			if err != nil {
				return err
			}
			is := image.NewStore(ref, sopts...)

			pf, done := ProgressHandler(ctx, os.Stdout)
			defer done()

			return client.Transfer(ctx, reg, is, transfer.WithProgress(pf))
		}

		ctx, done, err := client.WithLease(ctx)
		if err != nil {
			return err
		}
		defer done(ctx)

		// TODO: Handle this locally via transfer config
		config, err := content.NewFetchConfig(ctx, context)
		if err != nil {
			return err
		}

		img, err := content.Fetch(ctx, client, ref, config)
		if err != nil {
			return err
		}

		log.G(ctx).WithField("image", ref).Debug("unpacking")

		// TODO: Show unpack status

		var p []ocispec.Platform
		if context.Bool("all-platforms") {
			p, err = images.Platforms(ctx, client.ContentStore(), img.Target)
			if err != nil {
				return fmt.Errorf("unable to resolve image platforms: %w", err)
			}
		} else {
			p, err = platforms.ParseAll(context.StringSlice("platform"))
			if err != nil {
				return err
			}
		}
		if len(p) == 0 {
			p = append(p, platforms.DefaultSpec())
		}

		start := time.Now()
		for _, platform := range p {
			fmt.Printf("unpacking %s %s...\n", platforms.Format(platform), img.Target.Digest)
			i := containerd.NewImageWithPlatform(client, img, platforms.Only(platform))
			err = i.Unpack(ctx, context.String("snapshotter"))
			if err != nil {
				return err
			}
			if context.Bool("print-chainid") {
				diffIDs, err := i.RootFS(ctx)
				if err != nil {
					return err
				}
				chainID := identity.ChainID(diffIDs).String()
				fmt.Printf("image chain ID: %s\n", chainID)
			}
		}
		fmt.Printf("done: %s\t\n", time.Since(start))
		return nil
	},
}

type progressNode struct {
	transfer.Progress
	children []*progressNode
	root     bool
}

func (n *progressNode) mainDesc() *ocispec.Descriptor {
	if n.Desc != nil {
		return n.Desc
	}
	for _, c := range n.children {
		if desc := c.mainDesc(); desc != nil {
			return desc
		}
	}
	return nil
}

// ProgressHandler continuously updates the output with job progress
// by checking status in the content store.
func ProgressHandler(ctx context.Context, out io.Writer) (transfer.ProgressFunc, func()) {
	ctx, cancel := context.WithCancel(ctx)
	var (
		fw       = progress.NewWriter(out)
		start    = time.Now()
		statuses = map[string]*progressNode{}
		roots    = []*progressNode{}
		progress transfer.ProgressFunc
		pc       = make(chan transfer.Progress, 1)
		status   string
		closeC   = make(chan struct{})
	)

	progress = func(p transfer.Progress) {
		select {
		case pc <- p:
		case <-ctx.Done():
		}
	}

	done := func() {
		cancel()
		<-closeC
	}
	go func() {
		defer close(closeC)
		for {
			select {
			case p := <-pc:
				if p.Name == "" {
					status = p.Event
					continue
				}
				if node, ok := statuses[p.Name]; !ok {
					node = &progressNode{
						Progress: p,
						root:     true,
					}

					if len(p.Parents) == 0 {
						roots = append(roots, node)
					} else {
						var parents []string
						for _, parent := range p.Parents {
							pStatus, ok := statuses[parent]
							if ok {
								parents = append(parents, parent)
								pStatus.children = append(pStatus.children, node)
								node.root = false
							}
						}
						node.Progress.Parents = parents
						if node.root {
							roots = append(roots, node)
						}
					}
					statuses[p.Name] = node
				} else {
					if len(node.Progress.Parents) != len(p.Parents) {
						var parents []string
						var removeRoot bool
						for _, parent := range p.Parents {
							pStatus, ok := statuses[parent]
							if ok {
								parents = append(parents, parent)
								var found bool
								for _, child := range pStatus.children {

									if child.Progress.Name == p.Name {
										found = true
										break
									}
								}
								if !found {
									pStatus.children = append(pStatus.children, node)

								}
								if node.root {
									removeRoot = true
								}
								node.root = false
							}
						}
						p.Parents = parents
						// Check if needs to remove from root
						if removeRoot {
							for i := range roots {
								if roots[i] == node {
									roots = append(roots[:i], roots[i+1:]...)
									break
								}
							}
						}

					}
					node.Progress = p
				}

				/*
					all := make([]transfer.Progress, 0, len(statuses))
					for _, p := range statuses {
						all = append(all, p.Progress)
					}
					sort.Slice(all, func(i, j int) bool {
						return all[i].Name < all[j].Name
					})
					Display(fw, status, all, start)
				*/
				DisplayHierarchy(fw, status, roots, start)
				fw.Flush()
			case <-ctx.Done():
				return
			}
		}
	}()

	return progress, done
}

func DisplayHierarchy(w io.Writer, status string, roots []*progressNode, start time.Time) {
	total := displayNode(w, "", roots)
	for _, r := range roots {
		if desc := r.mainDesc(); desc != nil {
			fmt.Fprintf(w, "%s %s\n", desc.MediaType, desc.Digest)
		}
	}
	// Print the Status line
	fmt.Fprintf(w, "%s\telapsed: %-4.1fs\ttotal: %7.6v\t(%v)\t\n",
		status,
		time.Since(start).Seconds(),
		// TODO(stevvooe): These calculations are actually way off.
		// Need to account for previously downloaded data. These
		// will basically be right for a download the first time
		// but will be skewed if restarting, as it includes the
		// data into the start time before.
		progress.Bytes(total),
		progress.NewBytesPerSecond(total, time.Since(start)))
}

func displayNode(w io.Writer, prefix string, nodes []*progressNode) int64 {
	var total int64
	for i, node := range nodes {
		status := node.Progress
		total += status.Progress
		pf, cpf := prefixes(i, len(nodes))
		if node.root {
			pf, cpf = "", ""
		}

		name := prefix + pf + displayName(status.Name)

		switch status.Event {
		case "downloading", "uploading":
			var bar progress.Bar
			if status.Total > 0.0 {
				bar = progress.Bar(float64(status.Progress) / float64(status.Total))
			}
			fmt.Fprintf(w, "%-40.40s\t%-11s\t%40r\t%8.8s/%s\t\n",
				name,
				status.Event,
				bar,
				progress.Bytes(status.Progress), progress.Bytes(status.Total))
		case "resolving", "waiting":
			bar := progress.Bar(0.0)
			fmt.Fprintf(w, "%-40.40s\t%-11s\t%40r\t\n",
				name,
				status.Event,
				bar)
		case "complete":
			bar := progress.Bar(1.0)
			fmt.Fprintf(w, "%-40.40s\t%-11s\t%40r\t\n",
				name,
				status.Event,
				bar)
		default:
			fmt.Fprintf(w, "%-40.40s\t%s\t\n",
				name,
				status.Event)
		}
		total += displayNode(w, prefix+cpf, node.children)
	}
	return total
}

func prefixes(index, length int) (prefix string, childPrefix string) {
	if index+1 == length {
		prefix = "└──"
		childPrefix = "   "
	} else {
		prefix = "├──"
		childPrefix = "│  "
	}
	return
}

func displayName(name string) string {
	parts := strings.Split(name, "-")
	for i := range parts {
		parts[i] = shortenName(parts[i])
	}
	return strings.Join(parts, " ")
}

func shortenName(name string) string {
	if strings.HasPrefix(name, "sha256:") && len(name) == 71 {
		return "(" + name[7:19] + ")"
	}
	return name
}

// Display pretty prints out the download or upload progress
// Status tree
func Display(w io.Writer, status string, statuses []transfer.Progress, start time.Time) {
	var total int64
	for _, status := range statuses {
		total += status.Progress
		switch status.Event {
		case "downloading", "uploading":
			var bar progress.Bar
			if status.Total > 0.0 {
				bar = progress.Bar(float64(status.Progress) / float64(status.Total))
			}
			fmt.Fprintf(w, "%s:\t%s\t%40r\t%8.8s/%s\t\n",
				status.Name,
				status.Event,
				bar,
				progress.Bytes(status.Progress), progress.Bytes(status.Total))
		case "resolving", "waiting":
			bar := progress.Bar(0.0)
			fmt.Fprintf(w, "%s:\t%s\t%40r\t\n",
				status.Name,
				status.Event,
				bar)
		case "complete", "done":
			bar := progress.Bar(1.0)
			fmt.Fprintf(w, "%s:\t%s\t%40r\t\n",
				status.Name,
				status.Event,
				bar)
		default:
			fmt.Fprintf(w, "%s:\t%s\t\n",
				status.Name,
				status.Event)
		}
	}

	// Print the Status line
	fmt.Fprintf(w, "%s\telapsed: %-4.1fs\ttotal: %7.6v\t(%v)\t\n",
		status,
		time.Since(start).Seconds(),
		// TODO(stevvooe): These calculations are actually way off.
		// Need to account for previously downloaded data. These
		// will basically be right for a download the first time
		// but will be skewed if restarting, as it includes the
		// data into the start time before.
		progress.Bytes(total),
		progress.NewBytesPerSecond(total, time.Since(start)))
}
