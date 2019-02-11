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
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/images/encryption"
	"github.com/containerd/containerd/platforms"

	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var layerinfoCommand = cli.Command{
	Name:      "layerinfo",
	Usage:     "get information about an image's layers",
	ArgsUsage: "[flags] <local>",
	Description: `Get encryption information about the layers of an image.

	Get information about the layers of an image and display with which
	encryption technology the individual layers are encrypted with.
	The user has control over the individual layers and the platforms they are
	associated with and can retrieve information for them separately. If no
	layers or platforms are specified, infomration for all layers and all
	platforms will be retrieved.
`,
	Flags: append(commands.RegistryFlags, cli.IntSliceFlag{
		Name:  "layer",
		Usage: "The layer to get info for; this must be either the layer number or a negative number starting with -1 for topmost layer",
	}, cli.StringSliceFlag{
		Name:  "platform",
		Usage: "For which platform to get the layer info; by default info for all platforms is retrieved",
	}, cli.StringFlag{
		Name:  "gpg-homedir",
		Usage: "The GPG homedir to use; by default gpg uses ~/.gnupg",
	}, cli.StringFlag{
		Name:  "gpg-version",
		Usage: "The GPG version (\"v1\" or \"v2\"), default will make an educated guess",
	}, cli.BoolFlag{
		Name:  "n",
		Usage: "Do not resolve PGP key IDs to email addresses",
	}),
	Action: func(context *cli.Context) error {
		local := context.Args().First()
		if local == "" {
			return errors.New("please provide the name of an image to decrypt")
		}
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		layers32 := commands.IntToInt32Array(context.IntSlice("layer"))

		LayerInfos, _, err := getImageLayerInfos(client, ctx, local, layers32, context.StringSlice("platform"))
		if err != nil {
			return err
		}
		if len(LayerInfos) == 0 {
			return nil
		}

		var gpgClient encryption.GPGClient
		if !context.Bool("n") {
			// create a GPG client to resolve keyIds to names
			gpgClient, _ = createGPGClient(context)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', tabwriter.AlignRight)
		fmt.Fprintf(w, "#\tDIGEST\tPLATFORM\tSIZE\tENCRYPTION\tRECIPIENTS\t\n")
		for _, layer := range LayerInfos {
			var recipients []string
			var schemes []string
			for scheme, wrappedKeys := range encryption.GetWrappedKeysMap(layer.Descriptor) {
				schemes = append(schemes, scheme)
				keywrapper := encryption.GetKeyWrapper(scheme)
				if keywrapper != nil {
					addRecipients, err := keywrapper.GetRecipients(wrappedKeys)
					if err != nil {
						return err
					}
					if scheme == "pgp" && gpgClient != nil {
						addRecipients = gpgClient.ResolveRecipients(addRecipients)
					}
					recipients = append(recipients, addRecipients...)
				} else {
					recipients = append(recipients, fmt.Sprintf("No %s KeyWrapper", scheme))
				}
			}
			sort.Strings(schemes)
			sort.Strings(recipients)
			fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\t%s\t\n", layer.Index, layer.Descriptor.Digest.String(), platforms.Format(*layer.Descriptor.Platform), layer.Descriptor.Size, strings.Join(schemes, ","), strings.Join(recipients, ", "))
		}
		w.Flush()
		return nil
	},
}
