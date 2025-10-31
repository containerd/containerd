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

package content

import (
	gocontext "context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	units "github.com/docker/go-units"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli/v2"
)

var (
	// Command is the cli command for managing content
	Command = &cli.Command{
		Name:  "content",
		Usage: "Manage content",
		Subcommands: cli.Commands{
			activeIngestCommand,
			deleteCommand,
			editCommand,
			fetchCommand,
			fetchObjectCommand,
			fetchBlobCommand,
			getCommand,
			ingestCommand,
			listCommand,
			listReferencesCommand,
			pushObjectCommand,
			setLabelsCommand,
			pruneCommand,
		},
	}

	getCommand = &cli.Command{
		Name:        "get",
		Usage:       "Get the data for an object",
		ArgsUsage:   "[<digest>, ...]",
		Description: "display the image object",
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
			cs := client.ContentStore()
			ra, err := cs.ReaderAt(ctx, ocispec.Descriptor{Digest: dgst})
			if err != nil {
				return err
			}
			defer ra.Close()

			// use 1MB buffer like we do for ingesting
			buf := make([]byte, 1<<20)
			_, err = io.CopyBuffer(os.Stdout, content.NewReader(ra), buf)
			return err
		},
	}

	ingestCommand = &cli.Command{
		Name:        "ingest",
		Usage:       "Accept content into the store",
		ArgsUsage:   "[flags] <key>",
		Description: "ingest objects into the local content store",
		Flags: []cli.Flag{
			&cli.Int64Flag{
				Name:  "expected-size",
				Usage: "Validate against provided size",
			},
			&cli.StringFlag{
				Name:  "expected-digest",
				Usage: "Verify content against expected digest",
			},
		},
		Action: func(cliContext *cli.Context) error {
			var (
				ref            = cliContext.Args().First()
				expectedSize   = cliContext.Int64("expected-size")
				expectedDigest = digest.Digest(cliContext.String("expected-digest"))
			)
			if err := expectedDigest.Validate(); expectedDigest != "" && err != nil {
				return err
			}
			if ref == "" {
				return errors.New("must specify a transaction reference")
			}
			client, ctx, cancel, err := commands.NewClient(cliContext)
			if err != nil {
				return err
			}
			defer cancel()

			cs := client.ContentStore()

			// TODO(stevvooe): Allow ingest to be reentrant. Currently, we expect
			// all data to be written in a single invocation. Allow multiple writes
			// to the same transaction key followed by a commit.
			return content.WriteBlob(ctx, cs, ref, os.Stdin, ocispec.Descriptor{Size: expectedSize, Digest: expectedDigest})
		},
	}

	activeIngestCommand = &cli.Command{
		Name:        "active",
		Usage:       "Display active transfers",
		ArgsUsage:   "[flags] [<regexp>]",
		Description: "display the ongoing transfers",
		Flags: []cli.Flag{
			&cli.DurationFlag{
				Name:    "timeout",
				Aliases: []string{"t"},
				Usage:   "Total timeout for fetch",
				EnvVars: []string{"CONTAINERD_FETCH_TIMEOUT"},
			},
			&cli.StringFlag{
				Name:  "root",
				Usage: "Path to content store root",
				Value: "/tmp/content", // TODO(stevvooe): for now, just use the PWD/.content
			},
		},
		Action: func(cliContext *cli.Context) error {
			match := cliContext.Args().First()
			client, ctx, cancel, err := commands.NewClient(cliContext)
			if err != nil {
				return err
			}
			defer cancel()
			cs := client.ContentStore()
			active, err := cs.ListStatuses(ctx, match)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
			fmt.Fprintln(tw, "REF\tSIZE\tAGE\t")
			for _, active := range active {
				fmt.Fprintf(tw, "%s\t%s\t%s\t\n",
					active.Ref,
					units.HumanSize(float64(active.Offset)),
					units.HumanDuration(time.Since(active.StartedAt)))
			}

			return tw.Flush()
		},
	}

	listCommand = &cli.Command{
		Name:        "list",
		Aliases:     []string{"ls"},
		Usage:       "List all blobs in the store",
		ArgsUsage:   "[flags]",
		Description: "list blobs in the content store",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "quiet",
				Aliases: []string{"q"},
				Usage:   "Print only the blob digest",
			},
		},
		Action: func(cliContext *cli.Context) error {
			var (
				quiet = cliContext.Bool("quiet")
				args  = cliContext.Args().Slice()
			)
			client, ctx, cancel, err := commands.NewClient(cliContext)
			if err != nil {
				return err
			}
			defer cancel()
			cs := client.ContentStore()

			var walkFn content.WalkFunc
			if quiet {
				walkFn = func(info content.Info) error {
					fmt.Println(info.Digest)
					return nil
				}
			} else {
				tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
				defer tw.Flush()

				fmt.Fprintln(tw, "DIGEST\tSIZE\tAGE\tLABELS")
				walkFn = func(info content.Info) error {
					var labelStrings []string
					for k, v := range info.Labels {
						labelStrings = append(labelStrings, strings.Join([]string{k, v}, "="))
					}
					sort.Strings(labelStrings)
					labels := strings.Join(labelStrings, ",")
					if labels == "" {
						labels = "-"
					}

					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
						info.Digest,
						units.HumanSize(float64(info.Size)),
						units.HumanDuration(time.Since(info.CreatedAt)),
						labels)
					return nil
				}

			}

			return cs.Walk(ctx, walkFn, args...)
		},
	}

	listReferencesCommand = &cli.Command{
		Name:        "references",
		Usage:       "List references to the given content",
		ArgsUsage:   "<digest>",
		Description: "Return all references to the given piece of content",
		Action: func(context *cli.Context) error {
			var object = context.Args().First()
			client, ctx, cancel, err := commands.NewClient(context)
			if err != nil {
				return err
			}
			defer cancel()

			tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
			defer tw.Flush()

			fmt.Fprintln(tw, "TYPE\tID")

			// Images
			imgStore := client.ImageService()

			dgst, err := digest.Parse(object)
			if err != nil {
				return err
			}

			digestField := "target.digest"
			filter := fmt.Sprintf("%s==%s", digestField, dgst)

			imgs, err := imgStore.List(ctx, filter)
			if err != nil {
				return err
			}

			imgNames := make([]string, 0)
			for _, m := range imgs {
				imgNames = append(imgNames, m.Name)
			}

			// Content
			cs := client.ContentStore()

			ids := make([]string, 0)

			walkFn := func(info content.Info) error {
				var isRef = false
				for _, v := range info.Labels {
					if v == string(dgst) {
						isRef = true
						break
					}
				}
				if isRef {
					ids = append(ids, string(info.Digest))
				}
				return nil
			}

			args := make([]string, 0)
			err = cs.Walk(ctx, walkFn, args...)
			if err != nil {
				return err
			}

			// Lease
			leaseRefs := make([]string, 0)
			contentKey := "content"
			lm := client.LeasesService()

			leases, err := lm.List(ctx)
			if err != nil {
				return err
			}

			for _, lease := range leases {
				resources, err := lm.ListResources(ctx, lease)
				if err != nil {
					return err
				}

				for _, r := range resources {
					if r.Type != contentKey {
						continue
					}

					if r.ID == string(dgst) {
						leaseRefs = append(leaseRefs, lease.ID)
					}
				}
			}

			// Snapshots
			snapshotterRefs := make([]string, 0)
			snapshotSvc := client.SnapshotService(context.String("snapshotter"))
			snapWalkFn := func(_ gocontext.Context, info snapshots.Info) error {
				isRef := false
				for _, v := range info.Labels {
					if v == string(dgst) {
						isRef = true
						break
					}
				}
				if isRef {
					snapshotterRefs = append(snapshotterRefs, info.Name)
				}
				return nil
			}

			args = make([]string, 0)
			err = snapshotSvc.Walk(ctx, snapWalkFn, args...)
			if err != nil {
				return err
			}

			// Sandbox
			ss := client.SandboxStore()
			sandboxRefs := make([]string, 0)

			sandboxes, err := ss.List(ctx)
			if err != nil {
				return err
			}

			for _, sbox := range sandboxes {
				for _, v := range sbox.Labels {
					if v == string(dgst) {
						sandboxRefs = append(sandboxRefs, sbox.ID)
						break
					}
				}
			}

			// Container
			containerSvc := client.ContainerService()
			containerRefs := make([]string, 0)

			containerList, err := containerSvc.List(ctx)
			if err != nil {
				return err
			}

			for _, container := range containerList {
				for _, v := range container.Labels {
					if v == string(dgst) {
						containerRefs = append(containerRefs, container.ID)
						break
					}
				}
			}

			fmt.Fprintf(tw, "%s\t%s\n", "Containers", strings.Join(containerRefs, ", "))
			fmt.Fprintf(tw, "%s\t%s\n", "Content", strings.Join(ids, ", "))
			fmt.Fprintf(tw, "%s\t%s\n", "Images", strings.Join(imgNames, ", "))
			fmt.Fprintf(tw, "%s\t%s\n", "Leases", strings.Join(leaseRefs, ", "))
			fmt.Fprintf(tw, "%s\t%s\n", "Sandboxes", strings.Join(sandboxRefs, ", "))
			fmt.Fprintf(tw, "%s\t%s\n", "Snapshots", strings.Join(snapshotterRefs, ", "))

			return nil
		},
	}

	setLabelsCommand = &cli.Command{
		Name:        "label",
		Usage:       "Add labels to content",
		ArgsUsage:   "<digest> [<label>=<value> ...]",
		Description: "labels blobs in the content store",
		Action: func(cliContext *cli.Context) error {
			object, labels := commands.ObjectWithLabelArgs(cliContext)
			client, ctx, cancel, err := commands.NewClient(cliContext)
			if err != nil {
				return err
			}
			defer cancel()

			cs := client.ContentStore()

			dgst, err := digest.Parse(object)
			if err != nil {
				return err
			}

			info := content.Info{
				Digest: dgst,
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
				info, err = cs.Info(ctx, info.Digest)
			} else {
				info, err = cs.Update(ctx, info, paths...)
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

	editCommand = &cli.Command{
		Name:        "edit",
		Usage:       "Edit a blob and return a new digest",
		ArgsUsage:   "[flags] <digest>",
		Description: "edit a blob and return a new digest",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "validate",
				Usage: "Validate the result against a format (json, mediatype, etc.)",
			},
			&cli.StringFlag{
				Name:    "editor",
				Usage:   "Select editor (vim, emacs, etc.)",
				EnvVars: []string{"EDITOR"},
			},
		},
		Action: func(cliContext *cli.Context) error {
			var (
				validate = cliContext.String("validate")
				object   = cliContext.Args().First()
			)

			if validate != "" {
				return errors.New("validating the edit result not supported")
			}

			// TODO(stevvooe): Support looking up objects by a reference through
			// the image metadata storage.

			dgst, err := digest.Parse(object)
			if err != nil {
				return err
			}
			client, ctx, cancel, err := commands.NewClient(cliContext)
			if err != nil {
				return err
			}
			defer cancel()
			cs := client.ContentStore()
			ra, err := cs.ReaderAt(ctx, ocispec.Descriptor{Digest: dgst})
			if err != nil {
				return err
			}
			defer ra.Close()

			nrc, err := edit(cliContext, content.NewReader(ra))
			if err != nil {
				return err
			}
			defer nrc.Close()

			wr, err := cs.Writer(ctx, content.WithRef("edit-"+object)) // TODO(stevvooe): Choose a better key?
			if err != nil {
				return err
			}

			if _, err := io.Copy(wr, nrc); err != nil {
				return err
			}

			if err := wr.Commit(ctx, 0, wr.Digest()); err != nil {
				return err
			}

			fmt.Println(wr.Digest())
			return nil
		},
	}

	deleteCommand = &cli.Command{
		Name:      "delete",
		Aliases:   []string{"del", "remove", "rm"},
		Usage:     "Permanently delete one or more blobs",
		ArgsUsage: "[<digest>, ...]",
		Description: `Delete one or more blobs permanently. Successfully deleted
	blobs are printed to stdout.`,
		Action: func(cliContext *cli.Context) error {
			var (
				args      = cliContext.Args().Slice()
				exitError error
			)
			client, ctx, cancel, err := commands.NewClient(cliContext)
			if err != nil {
				return err
			}
			defer cancel()
			cs := client.ContentStore()

			for _, arg := range args {
				dgst, err := digest.Parse(arg)
				if err != nil {
					if exitError == nil {
						exitError = err
					}
					log.G(ctx).WithError(err).Errorf("could not delete %v", dgst)
					continue
				}

				if err := cs.Delete(ctx, dgst); err != nil {
					if !errdefs.IsNotFound(err) {
						if exitError == nil {
							exitError = err
						}
						log.G(ctx).WithError(err).Errorf("could not delete %v", dgst)
					}
					continue
				}

				fmt.Println(dgst)
			}

			return exitError
		},
	}

	// TODO(stevvooe): Create "multi-fetch" mode that just takes a remote
	// then receives object/hint lines on stdin, returning content as
	// needed.
	fetchObjectCommand = &cli.Command{
		Name:        "fetch-object",
		Usage:       "Retrieve objects from a remote",
		ArgsUsage:   "[flags] <remote> <object> [<hint>, ...]",
		Description: `Fetch objects by identifier from a remote.`,
		Flags:       commands.RegistryFlags,
		Action: func(cliContext *cli.Context) error {
			var (
				ref = cliContext.Args().First()
			)
			ctx, cancel := commands.AppContext(cliContext)
			defer cancel()

			resolver, err := commands.GetResolver(ctx, cliContext)
			if err != nil {
				return err
			}

			ctx = log.WithLogger(ctx, log.G(ctx).WithField("ref", ref))

			log.G(ctx).Tracef("resolving")
			name, desc, err := resolver.Resolve(ctx, ref)
			if err != nil {
				return err
			}
			fetcher, err := resolver.Fetcher(ctx, name)
			if err != nil {
				return err
			}

			log.G(ctx).Tracef("fetching")
			rc, err := fetcher.Fetch(ctx, desc)
			if err != nil {
				return err
			}
			defer rc.Close()

			_, err = io.Copy(os.Stdout, rc)
			return err
		},
	}

	fetchBlobCommand = &cli.Command{
		Name:        "fetch-blob",
		Usage:       "Retrieve blobs from a remote",
		ArgsUsage:   "[flags] <remote> [<digest>, ...]",
		Description: `Fetch blobs by digests from a remote.`,
		Flags: append(commands.RegistryFlags, []cli.Flag{
			&cli.StringFlag{
				Name:  "media-type",
				Usage: "Specify target mediatype for request header",
			},
		}...),
		Action: func(cliContext *cli.Context) error {
			var (
				ref     = cliContext.Args().First()
				digests = cliContext.Args().Tail()
			)
			if len(digests) == 0 {
				return errors.New("must specify digests")
			}
			ctx, cancel := commands.AppContext(cliContext)
			defer cancel()

			resolver, err := commands.GetResolver(ctx, cliContext)
			if err != nil {
				return err
			}

			ctx = log.WithLogger(ctx, log.G(ctx).WithField("ref", ref))

			log.G(ctx).Tracef("resolving")
			fetcher, err := resolver.Fetcher(ctx, ref)
			if err != nil {
				return err
			}

			fetcherByDigest, ok := fetcher.(remotes.FetcherByDigest)
			if !ok {
				return fmt.Errorf("fetcher %T does not implement remotes.FetcherByDigest", fetcher)
			}

			for _, f := range digests {
				dgst, err := digest.Parse(f)
				if err != nil {
					return err
				}
				rc, desc, err := fetcherByDigest.FetchByDigest(ctx, dgst, remotes.WithMediaType(cliContext.String("media-type")))
				if err != nil {
					return err
				}
				log.G(ctx).Debugf("desc=%+v", desc)
				_, err = io.Copy(os.Stdout, rc)
				rc.Close()
				if err != nil {
					return err
				}
			}
			return nil
		},
	}

	pushObjectCommand = &cli.Command{
		Name:        "push-object",
		Usage:       "Push an object to a remote",
		ArgsUsage:   "[flags] <remote> <object> <type>",
		Description: `Push objects by identifier to a remote.`,
		Flags:       commands.RegistryFlags,
		Action: func(cliContext *cli.Context) error {
			var (
				ref    = cliContext.Args().Get(0)
				object = cliContext.Args().Get(1)
				media  = cliContext.Args().Get(2)
			)
			dgst, err := digest.Parse(object)
			if err != nil {
				return err
			}
			client, ctx, cancel, err := commands.NewClient(cliContext)
			if err != nil {
				return err
			}
			defer cancel()

			resolver, err := commands.GetResolver(ctx, cliContext)
			if err != nil {
				return err
			}

			ctx = log.WithLogger(ctx, log.G(ctx).WithField("ref", ref))

			log.G(ctx).Tracef("resolving")
			pusher, err := resolver.Pusher(ctx, ref)
			if err != nil {
				return err
			}

			cs := client.ContentStore()

			info, err := cs.Info(ctx, dgst)
			if err != nil {
				return err
			}
			desc := ocispec.Descriptor{
				MediaType: media,
				Digest:    dgst,
				Size:      info.Size,
			}

			ra, err := cs.ReaderAt(ctx, desc)
			if err != nil {
				return err
			}
			defer ra.Close()

			cw, err := pusher.Push(ctx, desc)
			if err != nil {
				return err
			}

			// TODO: Progress reader
			if err := content.Copy(ctx, cw, content.NewReader(ra), desc.Size, desc.Digest); err != nil {
				return err
			}

			fmt.Printf("Pushed %s %s\n", desc.Digest, desc.MediaType)

			return nil
		},
	}
)

func edit(cliContext *cli.Context, rd io.Reader) (_ io.ReadCloser, retErr error) {
	editor := cliContext.String("editor")
	if editor == "" {
		return nil, errors.New("editor is required")
	}

	tmp, err := os.CreateTemp(os.Getenv("XDG_RUNTIME_DIR"), "edit-")
	if err != nil {
		return nil, err
	}

	defer func() {
		if retErr != nil {
			os.Remove(tmp.Name())
		}
	}()
	_, err = io.Copy(tmp, rd)
	tmp.Close()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("sh", "-c", fmt.Sprintf("%s %s", editor, tmp.Name()))

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		return nil, err
	}
	// The editor might recreate new file and override the original one. We should reopen the file
	edited, err := os.OpenFile(tmp.Name(), os.O_RDONLY, 0600)
	if err != nil {
		return nil, err
	}
	return onCloser{ReadCloser: edited, onClose: func() error {
		return os.RemoveAll(edited.Name())
	}}, nil
}

type onCloser struct {
	io.ReadCloser
	onClose func() error
}

func (oc onCloser) Close() error {
	var err error
	if err1 := oc.ReadCloser.Close(); err1 != nil {
		err = err1
	}

	if oc.onClose != nil {
		err1 := oc.onClose()
		if err == nil {
			err = err1
		}
	}

	return err
}
