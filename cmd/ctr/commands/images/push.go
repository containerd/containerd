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
	gocontext "context"
	"io"
	"io/ioutil"
	"net/http/httptrace"
	"os"
	"text/tabwriter"
	"time"

	pushapi "github.com/containerd/containerd/api/services/remotes/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/cmd/ctr/commands/content"
	"github.com/containerd/containerd/pkg/progress"
	"github.com/containerd/containerd/platforms"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
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
	Flags: append(commands.RegistryFlags, cli.StringFlag{
		Name:  "manifest",
		Usage: "digest of manifest",
	}, cli.StringFlag{
		Name:  "manifest-type",
		Usage: "media type of manifest digest",
		Value: ocispec.MediaTypeImageManifest,
	}, cli.StringSliceFlag{
		Name:  "platform",
		Usage: "push content from a specific platform",
		Value: &cli.StringSlice{},
	}, cli.IntFlag{
		Name:  "max-concurrent-uploaded-layers",
		Usage: "Set the max concurrent uploaded layers for each push",
	}),
	Action: func(context *cli.Context) error {
		var (
			ref   = context.Args().First()
			local = context.Args().Get(1)
			debug = context.GlobalBool("debug")
			desc  ocispec.Descriptor
		)
		if ref == "" {
			return errors.New("please provide a remote image reference to push")
		}

		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		if manifest := context.String("manifest"); manifest != "" {
			desc.Digest, err = digest.Parse(manifest)
			if err != nil {
				return errors.Wrap(err, "invalid manifest digest")
			}
			desc.MediaType = context.String("manifest-type")
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

		if context.Bool("http-trace") {
			ctx = httptrace.WithClientTrace(ctx, commands.NewDebugClientTrace(ctx))
		}

		var (
			platform *types.Platform
			p        v1.Platform
		)

		if pss := context.StringSlice("platform"); len(pss) == 1 {
			p, err = platforms.Parse(pss[0])
			if err != nil {
				return err
			}

			platform = &types.Platform{
				Architecture: p.Architecture,
				OS:           p.OS,
				Variant:      p.Variant,
			}
		}

		req := &pushapi.PushRequest{
			Source: types.Descriptor{
				Digest:    desc.Digest,
				MediaType: desc.MediaType,
				Size_:     desc.Size,
			},
			Target:         ref,
			MaxConcurrency: context.Int64("max-concurrent-uploaded-layers"),
			Platform:       platform,
		}

		username, secret, err := commands.GetAuth(context)
		if err != nil {
			return err
		}
		req.Auth = &pushapi.UserPassAuth{Username: username, Password: secret}

		ctx, cancel = gocontext.WithCancel(ctx)
		defer cancel()

		ch, err := client.PushService().Push(ctx, req)
		if err != nil {
			return err
		}

		// don't show progress if debug mode is set
		out := io.Writer(os.Stdout)
		if debug {
			out = ioutil.Discard
		}
		var (
			fw    = progress.NewWriter(out)
			start = time.Now()
			tw    = tabwriter.NewWriter(fw, 1, 8, 1, ' ', 0)
		)

		for status := range ch {
			fw.Flush()

			if status.Err != nil {
				return status.Err
			}
			if status.PushResponse != nil {
				content.Display(tw, apiPushStatusToContent(status.Statuses), start)
				tw.Flush()
			}
		}

		fw.Flush()

		return nil
	},
}

func apiPushStatusToContent(status []*pushapi.Status) []content.StatusInfo {
	ls := make([]content.StatusInfo, 0, len(status))
	for _, s := range status {
		si := content.StatusInfo{
			Ref:       s.Name,
			Offset:    s.Offset,
			Total:     s.Total,
			StartedAt: s.StartedAt,
			UpdatedAt: s.UpdatedAt,
		}

		switch s.Action {
		case pushapi.Action_Waiting:
			si.Status = "waiting"
		case pushapi.Action_Write:
			si.Status = "uploading"
		case pushapi.Action_Commit:
			si.Status = "committing"
		case pushapi.Action_Done:
			si.Status = "done"
		default:
			si.Status = s.Action.String()
		}

		ls = append(ls, si)
	}
	return ls
}
