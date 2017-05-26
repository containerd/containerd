package main

import (
	"context"
	"io"
	"os"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/progress"
	"github.com/containerd/containerd/remotes"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
	"golang.org/x/sync/errgroup"
)

var pushCommand = cli.Command{
	Name:      "push",
	Usage:     "push an image to a remote",
	ArgsUsage: "[flags] <remote> [<local>]",
	Description: `Pushes an image reference from containerd.

	All resources associated with the manifest reference will be pushed.
	The ref is used to resolve to a locally existing image manifest.
	The image manifest must exist before push. Creating a new image
	manifest can be done through calculating the diff for layers,
	creating the associated configuration, and creating the manifest
	which references those resources.
`,
	Flags: append(registryFlags, cli.StringFlag{
		Name:  "manifest",
		Usage: "Digest of manifest",
	}, cli.StringFlag{
		Name:  "manifest-type",
		Usage: "Media type of manifest digest",
		Value: ocispec.MediaTypeImageManifest,
	}),
	Action: func(clicontext *cli.Context) error {
		var (
			ref   = clicontext.Args().First()
			local = clicontext.Args().Get(1)
			desc  ocispec.Descriptor
		)

		ctx, cancel := appContext()
		defer cancel()

		client, err := getClient(clicontext)
		if err != nil {
			return err
		}

		if manifest := clicontext.String("manifest"); manifest != "" {
			desc.Digest, err = digest.Parse(manifest)
			if err != nil {
				return errors.Wrap(err, "invalid manifest digest")
			}
			desc.MediaType = clicontext.String("manifest-type")
		} else {
			if local == "" {
				local = ref
			}
			img, err := client.ImageService().Get(ctx, local)
			if err != nil {
				return errors.Wrap(err, "unable to resolve image to manifest")
			}
			desc = img.Target
		}

		resolver, err := getResolver(ctx, clicontext)
		if err != nil {
			return err
		}
		ongoing := newPushJobs()

		eg, ctx := errgroup.WithContext(ctx)

		eg.Go(func() error {
			log.G(ctx).WithField("image", ref).WithField("digest", desc.Digest).Debug("pushing")

			jobHandler := images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				ongoing.add(remotes.MakeRefKey(ctx, desc))
				return nil, nil
			})

			return client.Push(ctx, ref, desc,
				containerd.WithResolver(resolver),
				containerd.WithImageHandler(jobHandler),
				containerd.WithPushWrapper(ongoing.wrapPusher),
			)
		})

		errs := make(chan error)
		go func() {
			defer close(errs)
			errs <- eg.Wait()
		}()

		var (
			ticker = time.NewTicker(100 * time.Millisecond)
			fw     = progress.NewWriter(os.Stdout)
			start  = time.Now()
			done   bool
		)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				fw.Flush()

				tw := tabwriter.NewWriter(fw, 1, 8, 1, ' ', 0)

				display(tw, ongoing.status(), start)
				tw.Flush()

				if done {
					fw.Flush()
					return nil
				}
			case err := <-errs:
				if err != nil {
					return err
				}
				done = true
			case <-ctx.Done():
				done = true // allow ui to update once more
			}
		}
	},
}

type pushTracker struct {
	closed  bool
	started time.Time
	updated time.Time
	written int64
	total   int64
}

func (pt *pushTracker) Write(p []byte) (int, error) {
	pt.written += int64(len(p))
	pt.updated = time.Now()
	return len(p), nil
}

func (pt *pushTracker) Close() error {
	pt.closed = true
	pt.updated = time.Now()
	return nil
}

type pushWrapper struct {
	jobs   *pushjobs
	pusher remotes.Pusher
}

func (pw pushWrapper) Push(ctx context.Context, desc ocispec.Descriptor, r io.Reader) error {
	tr := pw.jobs.track(remotes.MakeRefKey(ctx, desc), desc.Size)
	defer tr.Close()
	return pw.pusher.Push(ctx, desc, io.TeeReader(r, tr))
}

type pushStatus struct {
	name    string
	started bool
	written int64
	total   int64
}

type pushjobs struct {
	jobs    map[string]*pushTracker
	ordered []string
	mu      sync.Mutex
}

func newPushJobs() *pushjobs {
	return &pushjobs{jobs: make(map[string]*pushTracker)}
}

func (j *pushjobs) wrapPusher(p remotes.Pusher) remotes.Pusher {
	return pushWrapper{
		jobs:   j,
		pusher: p,
	}
}

func (j *pushjobs) add(ref string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if _, ok := j.jobs[ref]; ok {
		return
	}
	j.ordered = append(j.ordered, ref)
	j.jobs[ref] = nil
}

func (j *pushjobs) track(ref string, size int64) io.WriteCloser {
	j.mu.Lock()
	defer j.mu.Unlock()

	if _, ok := j.jobs[ref]; !ok {
		j.ordered = append(j.ordered, ref)
	}

	pt := &pushTracker{
		started: time.Now(),
		total:   size,
	}
	j.jobs[ref] = pt
	return pt
}

func (j *pushjobs) status() []statusInfo {
	j.mu.Lock()
	defer j.mu.Unlock()

	status := make([]statusInfo, 0, len(j.jobs))
	for _, name := range j.ordered {
		tracker := j.jobs[name]
		si := statusInfo{
			Ref: name,
		}
		if tracker != nil {
			si.Offset = tracker.written
			si.Total = tracker.total
			si.StartedAt = tracker.started
			si.UpdatedAt = tracker.updated
			if tracker.closed {
				si.Status = "done"
			} else if tracker.written >= tracker.total {
				si.Status = "committing"
			} else {
				si.Status = "uploading"
			}
		} else {
			si.Status = "waiting"
		}
		status = append(status, si)
	}

	return status
}
