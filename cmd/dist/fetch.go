package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"text/tabwriter"
	"time"

	contentapi "github.com/containerd/containerd/api/services/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/progress"
	"github.com/containerd/containerd/remotes"
	contentservice "github.com/containerd/containerd/services/content"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli"
	"golang.org/x/sync/errgroup"
)

var fetchCommand = cli.Command{
	Name:      "fetch",
	Usage:     "fetch all content for an image into containerd",
	ArgsUsage: "[flags] <remote> <object>",
	Description: `Fetch an image into containerd.
	
This command ensures that containerd has all the necessary resources to build
an image's rootfs and convert the configuration to a runtime format supported
by containerd.

This command uses the same syntax, of remote and object, as 'dist
fetch-object'. We may want to make this nicer, but agnostism is preferred for
the moment.

Right now, the responsibility of the daemon and the cli aren't quite clear. Do
not use this implementation as a guide. The end goal should be having metadata,
content and snapshots ready for a direct use via the 'ctr run'.

Most of this is experimental and there are few leaps to make this work.`,
	Flags: registryFlags,
	Action: func(clicontext *cli.Context) error {
		var (
			ref = clicontext.Args().First()
		)
		ctx, cancel := appContext()
		defer cancel()

		conn, err := connectGRPC(clicontext)
		if err != nil {
			return err
		}

		resolver, err := getResolver(ctx, clicontext)
		if err != nil {
			return err
		}

		ongoing := newJobs()

		content := contentservice.NewStoreFromClient(contentapi.NewContentClient(conn))

		// TODO(stevvooe): Need to replace this with content store client.
		cs, err := resolveContentStore(clicontext)
		if err != nil {
			return err
		}

		eg, ctx := errgroup.WithContext(ctx)

		resolved := make(chan struct{})
		eg.Go(func() error {
			ongoing.add(ref)
			name, desc, fetcher, err := resolver.Resolve(ctx, ref)
			if err != nil {
				return err
			}
			log.G(ctx).WithField("image", name).Debug("fetching")
			close(resolved)

			return images.Dispatch(ctx,
				images.Handlers(images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
					ongoing.add(remotes.MakeRefKey(ctx, desc))
					return nil, nil
				}),
					remotes.FetchHandler(content, fetcher),
					images.ChildrenHandler(content),
				),
				desc)
		})

		errs := make(chan error)
		go func() {
			defer close(errs)
			errs <- eg.Wait()
		}()

		ticker := time.NewTicker(100 * time.Millisecond)
		fw := progress.NewWriter(os.Stdout)
		start := time.Now()
		defer ticker.Stop()
		var done bool

		for {
			select {
			case <-ticker.C:
				fw.Flush()

				tw := tabwriter.NewWriter(fw, 1, 8, 1, ' ', 0)

				statuses := map[string]statusInfo{}

				activeSeen := map[string]struct{}{}
				if !done {
					active, err := cs.Status(ctx, "")
					if err != nil {
						log.G(ctx).WithError(err).Error("active check failed")
						continue
					}
					// update status of active entries!
					for _, active := range active {
						statuses[active.Ref] = statusInfo{
							Ref:       active.Ref,
							Status:    "downloading",
							Offset:    active.Offset,
							Total:     active.Total,
							StartedAt: active.StartedAt,
							UpdatedAt: active.UpdatedAt,
						}
						activeSeen[active.Ref] = struct{}{}
					}
				}

				js := ongoing.jobs()
				// now, update the items in jobs that are not in active
				for _, j := range js {
					if _, ok := activeSeen[j]; ok {
						continue
					}
					status := "done"

					if j == ref {
						select {
						case <-resolved:
							status = "resolved"
						default:
							status = "resolving"
						}
					}

					statuses[j] = statusInfo{
						Ref:    j,
						Status: status, // for now!
					}
				}

				var ordered []statusInfo
				for _, j := range js {
					ordered = append(ordered, statuses[j])
				}

				display(tw, ordered, start)
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

// jobs provides a way of identifying the download keys for a particular task
// encountering during the pull walk.
//
// This is very minimal and will probably be replaced with something more
// featured.
type jobs struct {
	added map[string]struct{}
	refs  []string
	mu    sync.Mutex
}

func newJobs() *jobs {
	return &jobs{added: make(map[string]struct{})}
}

func (j *jobs) add(ref string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if _, ok := j.added[ref]; ok {
		return
	}
	j.refs = append(j.refs, ref)
	j.added[ref] = struct{}{}
}

func (j *jobs) jobs() []string {
	j.mu.Lock()
	defer j.mu.Unlock()

	var jobs []string
	return append(jobs, j.refs...)
}

type statusInfo struct {
	Ref       string
	Status    string
	Offset    int64
	Total     int64
	StartedAt time.Time
	UpdatedAt time.Time
}

func display(w io.Writer, statuses []statusInfo, start time.Time) {
	var total int64
	for _, status := range statuses {
		total += status.Offset
		switch status.Status {
		case "downloading":
			bar := progress.Bar(float64(status.Offset) / float64(status.Total))
			fmt.Fprintf(w, "%s:\t%s\t%40r\t%8.8s/%s\t\n",
				status.Ref,
				status.Status,
				bar,
				progress.Bytes(status.Offset), progress.Bytes(status.Total))
		case "resolving":
			bar := progress.Bar(0.0)
			fmt.Fprintf(w, "%s:\t%s\t%40r\t\n",
				status.Ref,
				status.Status,
				bar)
		default:
			bar := progress.Bar(1.0)
			fmt.Fprintf(w, "%s:\t%s\t%40r\t\n",
				status.Ref,
				status.Status,
				bar)
		}
	}

	fmt.Fprintf(w, "elapsed: %-4.1fs\ttotal: %7.6v\t(%v)\t\n",
		time.Since(start).Seconds(),
		// TODO(stevvooe): These calculations are actually way off.
		// Need to account for previously downloaded data. These
		// will basically be right for a download the first time
		// but will be skewed if restarting, as it includes the
		// data into the start time before.
		progress.Bytes(total),
		progress.NewBytesPerSecond(total, time.Since(start)))
}
