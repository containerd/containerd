package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	units "github.com/docker/go-units"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var (
	contentCommand = cli.Command{
		Name:  "content",
		Usage: "manage content",
		Subcommands: cli.Commands{
			listContentCommand,
			ingestContentCommand,
			activeIngestCommand,
			getContentCommand,
			editContentCommand,
			deleteContentCommand,
			labelContentCommand,
		},
	}

	getContentCommand = cli.Command{
		Name:        "get",
		Usage:       "get the data for an object",
		ArgsUsage:   "[flags] [<digest>, ...]",
		Description: "Display the image object.",
		Flags:       []cli.Flag{},
		Action: func(context *cli.Context) error {
			ctx, cancel := appContext(context)
			defer cancel()

			dgst, err := digest.Parse(context.Args().First())
			if err != nil {
				return err
			}

			cs, err := getContentStore(context)
			if err != nil {
				return err
			}

			ra, err := cs.ReaderAt(ctx, dgst)
			if err != nil {
				return err
			}
			defer ra.Close()

			_, err = io.Copy(os.Stdout, content.NewReader(ra))
			return err
		},
	}

	ingestContentCommand = cli.Command{
		Name:        "ingest",
		Usage:       "accept content into the store",
		ArgsUsage:   "[flags] <key>",
		Description: `Ingest objects into the local content store.`,
		Flags: []cli.Flag{
			cli.Int64Flag{
				Name:  "expected-size",
				Usage: "validate against provided size",
			},
			cli.StringFlag{
				Name:  "expected-digest",
				Usage: "verify content against expected digest",
			},
		},
		Action: func(context *cli.Context) error {
			var (
				ref            = context.Args().First()
				expectedSize   = context.Int64("expected-size")
				expectedDigest = digest.Digest(context.String("expected-digest"))
			)

			ctx, cancel := appContext(context)
			defer cancel()

			if err := expectedDigest.Validate(); expectedDigest != "" && err != nil {
				return err
			}

			if ref == "" {
				return errors.New("must specify a transaction reference")
			}

			cs, err := getContentStore(context)
			if err != nil {
				return err
			}

			// TODO(stevvooe): Allow ingest to be reentrant. Currently, we expect
			// all data to be written in a single invocation. Allow multiple writes
			// to the same transaction key followed by a commit.
			return content.WriteBlob(ctx, cs, ref, os.Stdin, expectedSize, expectedDigest)
		},
	}

	activeIngestCommand = cli.Command{
		Name:        "active",
		Usage:       "display active transfers.",
		ArgsUsage:   "[flags] [<regexp>]",
		Description: `Display the ongoing transfers.`,
		Flags: []cli.Flag{
			cli.DurationFlag{
				Name:   "timeout, t",
				Usage:  "total timeout for fetch",
				EnvVar: "CONTAINERD_FETCH_TIMEOUT",
			},
			cli.StringFlag{
				Name:  "root",
				Usage: "path to content store root",
				Value: "/tmp/content", // TODO(stevvooe): for now, just use the PWD/.content
			},
		},
		Action: func(context *cli.Context) error {
			var (
				match = context.Args().First()
			)

			ctx, cancel := appContext(context)
			defer cancel()

			cs, err := getContentStore(context)
			if err != nil {
				return err
			}

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

	listContentCommand = cli.Command{
		Name:        "list",
		Aliases:     []string{"ls"},
		Usage:       "list all blobs in the store.",
		ArgsUsage:   "[flags] [<filter>, ...]",
		Description: `List blobs in the content store.`,
		Flags: []cli.Flag{
			cli.BoolFlag{
				Name:  "quiet, q",
				Usage: "print only the blob digest",
			},
		},
		Action: func(context *cli.Context) error {
			var (
				quiet = context.Bool("quiet")
				args  = []string(context.Args())
			)
			ctx, cancel := appContext(context)
			defer cancel()

			cs, err := getContentStore(context)
			if err != nil {
				return err
			}

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

	labelContentCommand = cli.Command{
		Name:        "label",
		Usage:       "add labels to content",
		ArgsUsage:   "[flags] <digest> [<label>=<value> ...]",
		Description: `Labels blobs in the content store`,
		Flags:       []cli.Flag{},
		Action: func(context *cli.Context) error {
			var (
				object, labels = objectWithLabelArgs(context)
			)
			ctx, cancel := appContext(context)
			defer cancel()

			cs, err := getContentStore(context)
			if err != nil {
				return err
			}

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

	editContentCommand = cli.Command{
		Name:        "edit",
		Usage:       "edit a blob and return a new digest.",
		ArgsUsage:   "[flags] <digest>",
		Description: `Edit a blob and return a new digest.`,
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "validate",
				Usage: "validate the result against a format (json, mediatype, etc.)",
			},
		},
		Action: func(context *cli.Context) error {
			var (
				validate = context.String("validate")
				object   = context.Args().First()
			)
			ctx, cancel := appContext(context)
			defer cancel()

			if validate != "" {
				return errors.New("validating the edit result not supported")
			}

			// TODO(stevvooe): Support looking up objects by a reference through
			// the image metadata storage.

			dgst, err := digest.Parse(object)
			if err != nil {
				return err
			}

			cs, err := getContentStore(context)
			if err != nil {
				return err
			}

			ra, err := cs.ReaderAt(ctx, dgst)
			if err != nil {
				return err
			}
			defer ra.Close()

			nrc, err := edit(content.NewReader(ra))
			if err != nil {
				return err
			}
			defer nrc.Close()

			wr, err := cs.Writer(ctx, "edit-"+object, 0, "") // TODO(stevvooe): Choose a better key?
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

	deleteContentCommand = cli.Command{
		Name:      "delete",
		Aliases:   []string{"del", "remove", "rm"},
		Usage:     "permanently delete one or more blobs.",
		ArgsUsage: "[flags] [<digest>, ...]",
		Description: `Delete one or more blobs permanently. Successfully deleted
	blobs are printed to stdout.`,
		Flags: []cli.Flag{},
		Action: func(context *cli.Context) error {
			var (
				args      = []string(context.Args())
				exitError error
			)
			ctx, cancel := appContext(context)
			defer cancel()

			cs, err := getContentStore(context)
			if err != nil {
				return err
			}

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
)

func edit(rd io.Reader) (io.ReadCloser, error) {
	tmp, err := ioutil.TempFile("", "edit-")
	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(tmp, rd); err != nil {
		tmp.Close()
		return nil, err
	}

	cmd := exec.Command("sh", "-c", "$EDITOR "+tmp.Name())

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		tmp.Close()
		return nil, err
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		tmp.Close()
		return nil, err
	}

	return onCloser{ReadCloser: tmp, onClose: func() error {
		return os.RemoveAll(tmp.Name())
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
