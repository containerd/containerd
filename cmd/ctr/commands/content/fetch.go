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
	"context"
	"fmt"
	"io"
	"net/http/httptrace"
	"os"
	"sync"
	"text/tabwriter"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/content"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/images"
	"github.com/containerd/containerd/v2/pkg/progress"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/remotes"
	"github.com/containerd/log"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli"
)

var fetchCommand = cli.Command{
	Name:      "fetch",
	Usage:     "Fetch all content for an image into containerd",
	ArgsUsage: "[flags] <remote> <object>",
	Description: `Fetch an image into containerd.

This command ensures that containerd has all the necessary resources to build
an image's rootfs and convert the configuration to a runtime format supported
by containerd.

This command uses the same syntax, of remote and object, as 'ctr fetch-object'.
We may want to make this nicer, but agnostism is preferred for the moment.

Right now, the responsibility of the daemon and the cli aren't quite clear. Do
not use this implementation as a guide. The end goal should be having metadata,
content and snapshots ready for a direct use via the 'ctr run'.

Most of this is experimental and there are few leaps to make this work.`,
	Flags: append(commands.RegistryFlags, commands.LabelFlag,
		cli.StringSliceFlag{
			Name:  "platform",
			Usage: "Pull content from a specific platform",
		},
		cli.BoolFlag{
			Name:  "all-platforms",
			Usage: "Pull content from all platforms",
		},
		cli.BoolFlag{
			Name:   "all-metadata",
			Usage:  "(Deprecated: use skip-metadata) Pull metadata for all platforms",
			Hidden: true,
		},
		cli.BoolFlag{
			Name:  "skip-metadata",
			Usage: "Skips metadata for unused platforms (Image may be unable to be pushed without metadata)",
		},
		cli.BoolFlag{
			Name:  "metadata-only",
			Usage: "Pull all metadata including manifests and configs",
		},
	),
	Action: func(clicontext *cli.Context) error {
		var (
			ref = clicontext.Args().First()
		)
		client, ctx, cancel, err := commands.NewClient(clicontext)
		if err != nil {
			return err
		}
		defer cancel()
		config, err := NewFetchConfig(ctx, clicontext)
		if err != nil {
			return err
		}

		_, err = Fetch(ctx, client, ref, config)
		return err
	},
}

// FetchConfig for content fetch
type FetchConfig struct {
	// Resolver
	Resolver remotes.Resolver
	// ProgressOutput to display progress
	ProgressOutput io.Writer
	// Labels to set on the content
	Labels []string
	// PlatformMatcher matches platforms, supersedes Platforms
	PlatformMatcher platforms.MatchComparer
	// Platforms to fetch
	Platforms []string
	// Whether or not download all metadata
	AllMetadata bool
	// RemoteOpts to configure object resolutions and transfers with remote content providers
	RemoteOpts []containerd.RemoteOpt
	// TraceHTTP writes DNS and connection information to the log when dealing with a container registry
	TraceHTTP bool
}

// NewFetchConfig returns the default FetchConfig from cli flags
func NewFetchConfig(ctx context.Context, clicontext *cli.Context) (*FetchConfig, error) {
	resolver, err := commands.GetResolver(ctx, clicontext)
	if err != nil {
		return nil, err
	}
	config := &FetchConfig{
		Resolver:  resolver,
		Labels:    clicontext.StringSlice("label"),
		TraceHTTP: clicontext.Bool("http-trace"),
	}
	if !clicontext.GlobalBool("debug") {
		config.ProgressOutput = os.Stdout
	}
	if !clicontext.Bool("all-platforms") {
		p := clicontext.StringSlice("platform")
		if len(p) == 0 {
			p = append(p, platforms.DefaultString())
		}
		config.Platforms = p
	}

	if clicontext.Bool("metadata-only") {
		config.AllMetadata = true
		// Any with an empty set is None
		config.PlatformMatcher = platforms.Any()
	} else if !clicontext.Bool("skip-metadata") {
		config.AllMetadata = true
	}

	if clicontext.IsSet("max-concurrent-downloads") {
		mcd := clicontext.Int("max-concurrent-downloads")
		config.RemoteOpts = append(config.RemoteOpts, containerd.WithMaxConcurrentDownloads(mcd))
	}

	if clicontext.IsSet("max-concurrent-uploaded-layers") {
		mcu := clicontext.Int("max-concurrent-uploaded-layers")
		config.RemoteOpts = append(config.RemoteOpts, containerd.WithMaxConcurrentUploadedLayers(mcu))
	}

	return config, nil
}

// Fetch loads all resources into the content store and returns the image
func Fetch(ctx context.Context, client *containerd.Client, ref string, config *FetchConfig) (images.Image, error) {
	ongoing := NewJobs(ref)

	if config.TraceHTTP {
		ctx = httptrace.WithClientTrace(ctx, commands.NewDebugClientTrace(ctx))
	}

	pctx, stopProgress := context.WithCancel(ctx)
	progress := make(chan struct{})

	go func() {
		if config.ProgressOutput != nil {
			// no progress bar, because it hides some debug logs
			ShowProgress(pctx, ongoing, client.ContentStore(), config.ProgressOutput)
		}
		close(progress)
	}()

	h := images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		if desc.MediaType != images.MediaTypeDockerSchema1Manifest {
			ongoing.Add(desc)
		}
		return nil, nil
	})

	log.G(pctx).WithField("image", ref).Debug("fetching")
	labels := commands.LabelArgs(config.Labels)
	opts := []containerd.RemoteOpt{
		containerd.WithPullLabels(labels),
		containerd.WithResolver(config.Resolver),
		containerd.WithImageHandler(h),
		containerd.WithSchema1Conversion, //nolint:staticcheck // Ignore SA1019. Need to keep deprecated package for compatibility.
	}
	opts = append(opts, config.RemoteOpts...)

	if config.AllMetadata {
		opts = append(opts, containerd.WithAllMetadata())
	}

	if config.PlatformMatcher != nil {
		opts = append(opts, containerd.WithPlatformMatcher(config.PlatformMatcher))
	} else {
		for _, platform := range config.Platforms {
			opts = append(opts, containerd.WithPlatform(platform))
		}
	}

	img, err := client.Fetch(pctx, ref, opts...)
	stopProgress()
	if err != nil {
		return images.Image{}, err
	}

	<-progress
	return img, nil
}

// ShowProgress continuously updates the output with job progress
// by checking status in the content store.
func ShowProgress(ctx context.Context, ongoing *Jobs, cs content.Store, out io.Writer) {
	var (
		ticker   = time.NewTicker(100 * time.Millisecond)
		fw       = progress.NewWriter(out)
		start    = time.Now()
		statuses = map[string]StatusInfo{}
		done     bool
	)
	defer ticker.Stop()

outer:
	for {
		select {
		case <-ticker.C:
			fw.Flush()

			tw := tabwriter.NewWriter(fw, 1, 8, 1, ' ', 0)

			resolved := StatusResolved
			if !ongoing.IsResolved() {
				resolved = StatusResolving
			}
			statuses[ongoing.name] = StatusInfo{
				Ref:    ongoing.name,
				Status: resolved,
			}
			keys := []string{ongoing.name}

			activeSeen := map[string]struct{}{}
			if !done {
				active, err := cs.ListStatuses(ctx, "")
				if err != nil {
					log.G(ctx).WithError(err).Error("active check failed")
					continue
				}
				// update status of active entries!
				for _, active := range active {
					statuses[active.Ref] = StatusInfo{
						Ref:       active.Ref,
						Status:    StatusDownloading,
						Offset:    active.Offset,
						Total:     active.Total,
						StartedAt: active.StartedAt,
						UpdatedAt: active.UpdatedAt,
					}
					activeSeen[active.Ref] = struct{}{}
				}
			}

			// now, update the items in jobs that are not in active
			for _, j := range ongoing.Jobs() {
				key := remotes.MakeRefKey(ctx, j)
				keys = append(keys, key)
				if _, ok := activeSeen[key]; ok {
					continue
				}

				status, ok := statuses[key]
				if !done && (!ok || status.Status == StatusDownloading) {
					info, err := cs.Info(ctx, j.Digest)
					if err != nil {
						if !errdefs.IsNotFound(err) {
							log.G(ctx).WithError(err).Error("failed to get content info")
							continue outer
						} else {
							statuses[key] = StatusInfo{
								Ref:    key,
								Status: StatusWaiting,
							}
						}
					} else if info.CreatedAt.After(start) {
						statuses[key] = StatusInfo{
							Ref:       key,
							Status:    StatusDone,
							Offset:    info.Size,
							Total:     info.Size,
							UpdatedAt: info.CreatedAt,
						}
					} else {
						statuses[key] = StatusInfo{
							Ref:    key,
							Status: StatusExists,
						}
					}
				} else if done {
					if ok {
						if status.Status != StatusDone && status.Status != StatusExists {
							status.Status = StatusDone
							statuses[key] = status
						}
					} else {
						statuses[key] = StatusInfo{
							Ref:    key,
							Status: StatusDone,
						}
					}
				}
			}

			var ordered []StatusInfo
			for _, key := range keys {
				ordered = append(ordered, statuses[key])
			}

			Display(tw, ordered, start)
			tw.Flush()

			if done {
				fw.Flush()
				return
			}
		case <-ctx.Done():
			done = true // allow ui to update once more
		}
	}
}

// Jobs provides a way of identifying the download keys for a particular task
// encountering during the pull walk.
//
// This is very minimal and will probably be replaced with something more
// featured.
type Jobs struct {
	name     string
	added    map[digest.Digest]struct{}
	descs    []ocispec.Descriptor
	mu       sync.Mutex
	resolved bool
}

// NewJobs creates a new instance of the job status tracker
func NewJobs(name string) *Jobs {
	return &Jobs{
		name:  name,
		added: map[digest.Digest]struct{}{},
	}
}

// Add adds a descriptor to be tracked
func (j *Jobs) Add(desc ocispec.Descriptor) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.resolved = true

	if _, ok := j.added[desc.Digest]; ok {
		return
	}
	j.descs = append(j.descs, desc)
	j.added[desc.Digest] = struct{}{}
}

// Jobs returns a list of all tracked descriptors
func (j *Jobs) Jobs() []ocispec.Descriptor {
	j.mu.Lock()
	defer j.mu.Unlock()

	var descs []ocispec.Descriptor
	return append(descs, j.descs...)
}

// IsResolved checks whether a descriptor has been resolved
func (j *Jobs) IsResolved() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.resolved
}

// StatusInfoStatus describes status info for an upload or download.
type StatusInfoStatus string

const (
	StatusResolved    StatusInfoStatus = "resolved"
	StatusResolving   StatusInfoStatus = "resolving"
	StatusWaiting     StatusInfoStatus = "waiting"
	StatusCommitting  StatusInfoStatus = "committing"
	StatusDone        StatusInfoStatus = "done"
	StatusDownloading StatusInfoStatus = "downloading"
	StatusUploading   StatusInfoStatus = "uploading"
	StatusExists      StatusInfoStatus = "exists"
)

// StatusInfo holds the status info for an upload or download
type StatusInfo struct {
	Ref       string
	Status    StatusInfoStatus
	Offset    int64
	Total     int64
	StartedAt time.Time
	UpdatedAt time.Time
}

// Display pretty prints out the download or upload progress
func Display(w io.Writer, statuses []StatusInfo, start time.Time) {
	var total int64
	for _, status := range statuses {
		total += status.Offset
		switch status.Status {
		case StatusDownloading, StatusUploading:
			var bar progress.Bar
			if status.Total > 0.0 {
				bar = progress.Bar(float64(status.Offset) / float64(status.Total))
			}
			fmt.Fprintf(w, "%s:\t%s\t%40r\t%8.8s/%s\t\n",
				status.Ref,
				status.Status,
				bar,
				progress.Bytes(status.Offset), progress.Bytes(status.Total))
		case StatusResolving, StatusWaiting:
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
