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
	"os"
	"text/tabwriter"
	"time"

	"github.com/containerd/containerd"
	api "github.com/containerd/containerd/api/services/remotes/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/progress"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli"
)

var fetchCommand = cli.Command{
	Name:      "fetch",
	Usage:     "fetch all content for an image into containerd",
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
			Usage: "pull content from all platforms",
		},
		cli.BoolFlag{
			Name:  "all-metadata",
			Usage: "Pull metadata for all platforms",
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
	Auth      *api.UserPassAuth
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
	} else if clicontext.Bool("all-metadata") {
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
	var p *types.Platform
	if len(config.Platforms) > 0 {
		pl, err := platforms.Parse(config.Platforms[0])
		if err != nil {
			return images.Image{}, err
		}
		p = &types.Platform{
			OS:           pl.OS,
			Architecture: pl.Architecture,
			Variant:      pl.Variant,
		}
	}

	start := time.Now()
	ch, err := client.PullService().Pull(ctx, &api.PullRequest{
		Remote:   ref,
		Platform: p,
		Auth:     config.Auth,
		// AllMetadata: config.AllMetadata,
	})
	if err != nil {
		return images.Image{}, err
	}

	var img images.Image

	out := config.ProgressOutput
	if out == nil {
		out = io.Discard
	}
	fw := progress.NewWriter(out)
	tw := tabwriter.NewWriter(fw, 1, 8, 1, ' ', 0)

	for status := range ch {
		status := status

		fw.Flush()
		if status.Err != nil {
			return images.Image{}, status.Err
		}

		if status != nil {
			Display(tw, apiPushStatusToContent(ctx, status.Statuses), start)
			tw.Flush()
		}
		if status.Image != nil {
			img = apiToImage(*status.Image)
		}
	}

	fw.Flush()

	log.G(ctx).Debugf("+%v", img)

	return img, nil
}

func apiPushStatusToContent(ctx context.Context, status []*api.Status) []StatusInfo {
	ls := make([]StatusInfo, 0, len(status))

	for _, s := range status {
		si := StatusInfo{
			Ref:       s.Name,
			Offset:    s.Offset,
			Total:     s.Total,
			StartedAt: s.StartedAt,
			UpdatedAt: s.UpdatedAt,
		}

		switch s.Action {
		case api.Action_Resolving:
			si.Status = "resolving"
		case api.Action_Resolved:
			si.Status = "resolved"
		case api.Action_Waiting:
			si.Status = "waiting"
		case api.Action_Write:
			si.Status = "downloading"
		case api.Action_Commit:
			si.Status = "committing"
		case api.Action_Done:
			si.Status = "done"
		default:
			si.Status = s.Action.String()
		}

		ls = append(ls, si)
	}
	return ls
}

func apiToImage(img api.Image) images.Image {
	return images.Image{
		Name:   img.Name,
		Labels: img.Labels,
		Target: v1.Descriptor{
			MediaType:   img.Target.MediaType,
			Digest:      img.Target.Digest,
			Size:        img.Target.Size_,
			Annotations: img.Target.Annotations,
		},
	}
}

// StatusInfo holds the status info for an upload or download
type StatusInfo struct {
	Ref       string
	Status    string
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
		case "downloading", "uploading":
			var bar progress.Bar
			if status.Total > 0.0 {
				bar = progress.Bar(float64(status.Offset) / float64(status.Total))
			}
			fmt.Fprintf(w, "%s:\t%s\t%40r\t%8.8s/%s\t\n",
				status.Ref,
				status.Status,
				bar,
				progress.Bytes(status.Offset), progress.Bytes(status.Total))
		case "resolving", "waiting":
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
