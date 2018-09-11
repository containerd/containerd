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
	"fmt"
	"io"
	"os"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/images/docker"
	oci "github.com/containerd/containerd/images/oci"
	"github.com/containerd/containerd/log"
	"github.com/urfave/cli"
)

var importCommand = cli.Command{
	Name:      "import",
	Usage:     "import images",
	ArgsUsage: "[flags] <in>",
	Description: `Import images from a tar stream.
Implemented formats:
- oci.v1
- docker.v1.1
- docker.v1.2


For OCI v1, you may need to specify --base-name because an OCI archive
contains only partial image references (tags without the base image name).
If no base image name is provided, a name will be generated as "import-%{date}".

e.g.
  $ ctr images import --format oci.v1 --oci-name foo/bar foobar.tar

If foobar.tar contains an OCI ref named "latest" and anonymous ref "sha256:deadbeef", the command will create
"foo/bar:latest" and "foo/bar@sha256:deadbeef" images in the containerd store.
`,
	Flags: append([]cli.Flag{
		cli.StringFlag{
			Name:  "format",
			Value: "",
			Usage: "image format, by default supports OCI v1, Docker v1.1, Docker v1.2",
		},
		cli.StringFlag{
			Name:  "base-name,oci-name",
			Value: "",
			Usage: "base image name for added images, when provided images without this name prefix are filtered out",
		},
		cli.BoolFlag{
			Name:  "digests",
			Usage: "whether to create digest images",
		},
		cli.StringFlag{
			Name:  "index-name",
			Usage: "image name to keep index as, by default index is discarded",
		},
	}, commands.SnapshotterFlags...),

	Action: func(context *cli.Context) error {
		var (
			in   = context.Args().First()
			opts []containerd.ImportOpt
		)

		prefix := context.String("base-name")
		if prefix == "" {
			prefix = fmt.Sprintf("import-%s", time.Now().Format("2006-01-02"))
		}

		switch format := context.String("format"); format {
		case "", "docker", "docker.v1.1", "docker.v1.2":
			opts = append(opts, containerd.WithImporter(&docker.V1Importer{}))
			opts = append(opts, containerd.WithImageRefTranslator(docker.RefTranslator(prefix, context.String("base-name") != "")))
		case "oci", "oci.v1":
			opts = append(opts, containerd.WithImporter(&oci.V1Importer{}))
			opts = append(opts, containerd.WithImageRefTranslator(oci.RefTranslator(prefix)))
		default:
			return fmt.Errorf("unknown format %s", format)
		}

		if context.Bool("digests") {
			opts = append(opts, containerd.WithDigestRef(oci.DigestTranslator(prefix)))
		}

		if idxName := context.String("index-name"); idxName != "" {
			opts = append(opts, containerd.WithIndexName(idxName))
		}

		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		var r io.ReadCloser
		if in == "-" {
			r = os.Stdin
		} else {
			r, err = os.Open(in)
			if err != nil {
				return err
			}
		}
		imgs, err := client.Import(ctx, r, opts...)
		closeErr := r.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}

		log.G(ctx).Debugf("unpacking %d images", len(imgs))

		for _, img := range imgs {
			// TODO: Allow configuration of the platform
			image := containerd.NewImage(client, img)

			// TODO: Show unpack status
			fmt.Printf("unpacking %s (%s)...", img.Name, img.Target.Digest)
			err = image.Unpack(ctx, context.String("snapshotter"))
			if err != nil {
				return err
			}
			fmt.Println("done")
		}
		return nil
	},
}
