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

	"github.com/containerd/containerd/cmd/ctr/commands"
	imgenc "github.com/containerd/containerd/images/encryption"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var decryptCommand = cli.Command{
	Name:      "decrypt",
	Usage:     "decrypt an image locally",
	ArgsUsage: "[flags] <local> <new name>",
	Description: `Decrypt an image locally.

	Decrypt an image using private keys.
	The user has contol over which layers to decrypt and for which platform.
	If no payers or platforms are specified, all layers for all platforms are
	decrypted.

	Private keys in PEM format may be encrypted and the password may be passed
	along in any of the following formats:
	- <filename>:<password>
	- <filename>:pass=<password>
	- <filename>:fd=<file descriptor>
	- <filename>:filename=<password file>
`,
	Flags: append(append(commands.RegistryFlags, cli.IntSliceFlag{
		Name:  "layer",
		Usage: "The layer to decrypt; this must be either the layer number or a negative number starting with -1 for topmost layer",
	}, cli.StringSliceFlag{
		Name:  "platform",
		Usage: "For which platform to decrypt; by default decryption is done for all platforms",
	},
	), commands.ImageDecryptionFlags...),
	Action: func(context *cli.Context) error {
		local := context.Args().First()
		if local == "" {
			return errors.New("please provide the name of an image to decrypt")
		}

		newName := context.Args().Get(1)
		if newName != "" {
			fmt.Printf("Decrypting %s to %s\n", local, newName)
		} else {
			fmt.Printf("Decrypting %s and replacing it with the decrypted image\n", local)
		}
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		layers32 := commands.IntToInt32Array(context.IntSlice("layer"))

		_, descs, err := getImageLayerInfos(client, ctx, local, layers32, context.StringSlice("platform"))
		if err != nil {
			return err
		}

		isEncrypted := imgenc.HasEncryptedLayer(ctx, descs)
		if !isEncrypted {
			fmt.Printf("Nothing to decrypted.\n")
			return nil
		}

		cc, err := CreateDecryptCryptoConfig(context, descs)
		if err != nil {
			return err
		}

		_, err = decryptImage(client, ctx, local, newName, &cc, layers32, context.StringSlice("platform"))

		return err
	},
}
