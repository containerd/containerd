package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/Sirupsen/logrus"
	contentapi "github.com/docker/containerd/api/services/content"
	"github.com/docker/containerd/content"
	"github.com/docker/containerd/log"
	"github.com/docker/containerd/progress"
	"github.com/docker/containerd/remotes"
	contentservice "github.com/docker/containerd/services/content"
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
	Flags: []cli.Flag{},
	Action: func(clicontext *cli.Context) error {
		var (
			ctx = background
			ref = clicontext.Args().First()
		)

		conn, err := connectGRPC(clicontext)
		if err != nil {
			return err
		}

		resolver, err := getResolver(ctx)
		if err != nil {
			return err
		}
		ctx = withJobsContext(ctx)

		ingester := contentservice.NewIngesterFromClient(contentapi.NewContentClient(conn))
		cs, err := resolveContentStore(clicontext)
		if err != nil {
			return err
		}

		eg, ctx := errgroup.WithContext(ctx)

		resolved := make(chan struct{})
		eg.Go(func() error {
			addJob(ctx, ref)
			name, desc, fetcher, err := resolver.Resolve(ctx, ref)
			if err != nil {
				return err
			}
			log.G(ctx).WithField("image", name).Debug("fetching")
			close(resolved)

			return dispatch(ctx, ingester, fetcher, desc)
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
				var total int64

				js := getJobs(ctx)
				type statusInfo struct {
					Ref       string
					Status    string
					Offset    int64
					Total     int64
					StartedAt time.Time
					UpdatedAt time.Time
				}
				statuses := map[string]statusInfo{}

				activeSeen := map[string]struct{}{}
				if !done {
					active, err := cs.Active()
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

				for _, j := range js {
					status := statuses[j]

					total += status.Offset
					switch status.Status {
					case "downloading":
						bar := progress.Bar(float64(status.Offset) / float64(status.Total))
						fmt.Fprintf(tw, "%s:\t%s\t%40r\t%8.8s/%s\t\n",
							status.Ref,
							status.Status,
							bar,
							progress.Bytes(status.Offset), progress.Bytes(status.Total))
					case "resolving":
						bar := progress.Bar(0.0)
						fmt.Fprintf(tw, "%s:\t%s\t%40r\t\n",
							status.Ref,
							status.Status,
							bar)
					default:
						bar := progress.Bar(1.0)
						fmt.Fprintf(tw, "%s:\t%s\t%40r\t\n",
							status.Ref,
							status.Status,
							bar)
					}
				}

				fmt.Fprintf(tw, "elapsed: %-4.1fs\ttotal: %7.6v\t(%v)\t\n",
					time.Since(start).Seconds(),
					// TODO(stevvooe): These calculations are actually way off.
					// Need to account for previously downloaded data. These
					// will basically be right for a download the first time
					// but will be skewed if restarting, as it includes the
					// data into the start time before.
					progress.Bytes(total),
					progress.NewBytesPerSecond(total, time.Since(start)))
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

		return nil
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

// jobsKeys let's us store the jobs instance in the context.
//
// This is a very cute way to do things but not ideal.
type jobsKey struct{}

func getJobs(ctx context.Context) []string {
	return ctx.Value(jobsKey{}).(*jobs).jobs()
}

func addJob(ctx context.Context, job string) {
	ctx.Value(jobsKey{}).(*jobs).add(job)
}

func withJobsContext(ctx context.Context) context.Context {
	jobs := newJobs()
	return context.WithValue(ctx, jobsKey{}, jobs)
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
	for _, j := range j.refs {
		jobs = append(jobs, j)
	}

	return jobs
}

func fetchManifest(ctx context.Context, ingester content.Ingester, fetcher remotes.Fetcher, desc ocispec.Descriptor) error {
	ref := "manifest-" + desc.Digest.String()
	addJob(ctx, ref)

	rc, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		return err
	}
	defer rc.Close()

	// it would be better to read the content back from the content store in this case.
	p, err := ioutil.ReadAll(rc)
	if err != nil {
		return err
	}

	if err := content.WriteBlob(ctx, ingester, ref, bytes.NewReader(p), 0, ""); err != nil {
		return err
	}

	// TODO(stevvooe): This assumption that we get a manifest is unfortunate.
	// Need to provide way to resolve what the type of the target is.
	var manifest ocispec.Manifest
	if err := json.Unmarshal(p, &manifest); err != nil {
		return err
	}

	var descs []ocispec.Descriptor

	descs = append(descs, manifest.Config)
	for _, desc := range manifest.Layers {
		descs = append(descs, desc)
	}

	return dispatch(ctx, ingester, fetcher, descs...)
}

func fetch(ctx context.Context, ingester content.Ingester, fetcher remotes.Fetcher, desc ocispec.Descriptor) error {
	ref := "fetch-" + desc.Digest.String()
	addJob(ctx, ref)
	log.G(ctx).Debug("fetch")
	rc, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		log.G(ctx).WithError(err).Error("fetch error")
		return err
	}
	defer rc.Close()

	// TODO(stevvooe): Need better remote key selection here. Should be a
	// product of the fetcher. We may need more narrow infomation on fetcher or
	// just pull from env/context.
	return content.WriteBlob(ctx, ingester, ref, rc, desc.Size, desc.Digest)
}

// dispatch blocks until all content in `descs` is retrieved.
func dispatch(ctx context.Context, ingester content.Ingester, fetcher remotes.Fetcher, descs ...ocispec.Descriptor) error {
	eg, ctx := errgroup.WithContext(ctx)
	for _, desc := range descs {
		if err := func(desc ocispec.Descriptor) error {
			ctx := log.WithLogger(ctx, log.G(ctx).WithFields(logrus.Fields{
				"digest":    desc.Digest,
				"mediatype": desc.MediaType,
				"size":      desc.Size,
			}))
			switch desc.MediaType {
			case MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
				eg.Go(func() error {
					return fetchManifest(ctx, ingester, fetcher, desc)
				})
			case MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
				return fmt.Errorf("%v not yet supported", desc.MediaType)
			default:
				eg.Go(func() error {
					return fetch(ctx, ingester, fetcher, desc)
				})
			}

			return nil
		}(desc); err != nil {
			return err
		}
	}

	return eg.Wait()
}
