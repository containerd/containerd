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

	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/images"
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
- oci.v1     (default)


For oci.v1 format, you need to specify --oci-name because an OCI archive contains image refs (tags)
but does not contain the base image name.

e.g.
  $ ctr images import --format oci.v1 --oci-name foo/bar foobar.tar

If foobar.tar contains an OCI ref named "latest" and anonymous ref "sha256:deadbeef", the command will create
"foo/bar:latest" and "foo/bar@sha256:deadbeef" images in the containerd store.
`,
	Flags: append([]cli.Flag{
		cli.StringFlag{
			Name:  "format",
			Value: "oci.v1",
			Usage: "image format. See DESCRIPTION.",
		},
		cli.StringFlag{
			Name:  "oci-name",
			Value: "unknown/unknown",
			Usage: "prefix added to either oci.v1 ref annotation or digest",
		},
		// TODO(AkihiroSuda): support commands.LabelFlag (for all children objects)
	}, commands.SnapshotterFlags...),

	Action: func(context *cli.Context) error {
		var (
			in            = context.Args().First()
			imageImporter images.Importer
		)

		switch format := context.String("format"); format {
		case "oci.v1":
			imageImporter = &oci.V1Importer{
				ImageName: context.String("oci-name"),
			}
		default:
			return fmt.Errorf("unknown format %s", format)
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
		imgs, err := client.Import(ctx, imageImporter, r)
		if err != nil {
			return err
		}
		if err = r.Close(); err != nil {
			return err
		}

		log.G(ctx).Debugf("unpacking %d images", len(imgs))

		for _, img := range imgs {
			// TODO: Show unpack status
			fmt.Printf("unpacking %s (%s)...", img.Name(), img.Target().Digest)
			err = img.Unpack(ctx, context.String("snapshotter"))
			if err != nil {
				return err
			}
			fmt.Println("done")
		}
		return nil
	},
}
