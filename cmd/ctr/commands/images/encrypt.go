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
	encconfig "github.com/containerd/containerd/pkg/encryption/config"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var encryptCommand = cli.Command{
	Name:      "encrypt",
	Usage:     "encrypt an image locally",
	ArgsUsage: "[flags] <local> <new name>",
	Description: `Encrypt an image locally.

	Encrypt an image using public keys managed by GPG.
	The user must provide recpients who will be able to decrypt the image using
	their GPG-managed private key. For this the user's GPG keyring must hold the public
	keys of the recipients.
	The user has control over the individual layers and the platforms they are
	associated with and can encrypt them separately. If no layers or platforms are
	specified, all layers for all platforms will be encrypted.
	This tool also allows management of the recipients of the image through changes
	to the list of recipients.
	Once the image has been encrypted it may be pushed to a registry.

    Recipients are declared with the protocol prefix as follows:
    - pgp:<email-address>
    - jwe:<public-key-file-path>
    - pkcs7:<x509-file-path>
`,
	Flags: append(append(commands.RegistryFlags, cli.StringSliceFlag{
		Name:  "recipient",
		Usage: "Recipient of the image is the person who can decrypt it in the form specified above (i.e. jwe:/path/to/key)",
	}, cli.IntSliceFlag{
		Name:  "layer",
		Usage: "The layer to encrypt; this must be either the layer number or a negative number starting with -1 for topmost layer",
	}, cli.StringSliceFlag{
		Name:  "platform",
		Usage: "For which platform to encrypt; by default encrytion is done for all platforms",
	}), commands.ImageDecryptionFlags...),
	Action: func(context *cli.Context) error {
		local := context.Args().First()
		if local == "" {
			return errors.New("please provide the name of an image to encrypt")
		}

		newName := context.Args().Get(1)
		if newName != "" {
			fmt.Printf("Encrypting %s to %s\n", local, newName)
		} else {
			fmt.Printf("Encrypting %s and replacing it with the encrypted image\n", local)
		}
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		recipients := context.StringSlice("recipient")
		if len(recipients) == 0 {
			return errors.New("no recipients given -- nothing to do")
		}

		layers32 := commands.IntToInt32Array(context.IntSlice("layer"))

		gpgRecipients, pubKeys, x509s, err := processRecipientKeys(recipients)
		if err != nil {
			return err
		}

		_, descs, err := getImageLayerInfos(client, ctx, local, layers32, context.StringSlice("platform"))
		if err != nil {
			return err
		}

		encryptCcs := []encconfig.CryptoConfig{}
		_, err = createGPGClient(context)
		gpgInstalled := err == nil

		if len(gpgRecipients) > 0 && gpgInstalled {
			gpgClient, err := createGPGClient(context)
			if err != nil {
				return err
			}

			gpgPubRingFile, err := gpgClient.ReadGPGPubRingFile()
			if err != nil {
				return err
			}

			gpgCc, err := encconfig.EncryptWithGpg(gpgRecipients, gpgPubRingFile)
			if err != nil {
				return err
			}
			encryptCcs = append(encryptCcs, gpgCc)

		}

		// Create Encryption Crypto Config
		pkcs7Cc, err := encconfig.EncryptWithPkcs7(x509s)
		if err != nil {
			return err
		}
		encryptCcs = append(encryptCcs, pkcs7Cc)

		jweCc, err := encconfig.EncryptWithJwe(pubKeys)
		if err != nil {
			return err
		}
		encryptCcs = append(encryptCcs, jweCc)

		cc := encconfig.CombineCryptoConfigs(encryptCcs)

		// Create Decryption CryptoConfig for use in adding recipients to
		// existing image if decryptable.
		decryptCc, err := CreateDecryptCryptoConfig(context, descs)
		if err != nil {
			return err
		}
		cc.EncryptConfig.AttachDecryptConfig(decryptCc.DecryptConfig)

		_, err = encryptImage(client, ctx, local, newName, &cc, layers32, context.StringSlice("platform"))

		return err
	},
}
