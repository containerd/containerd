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

package decryptlayerstream

import (
	"bufio"
	"io"
	"os"
	"syscall"

	"github.com/containerd/containerd/cmd/ctr-layertool/commands/utils"
	"github.com/containerd/containerd/images/encryption"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

// Command is the cli command for managing images
var Command = cli.Command{
	Name:    "decrypt-layer-stream",
	Aliases: []string{"dls"},
	Usage:   "decrypt a layer stream",
	Description: `Decrypt a layer.

	Decrypt a layer. The encrypted layer data stream is expected on stdin
	and the decrypted layer data stream is produced on stdout. The
	parameters necessary to decrypt the layer must be passed using file
	descriptor 3 on Unix systems or the path of a named pipe using the
	environment variable STREAM_PROCESSOR_PIPE on Windows.
`,
	Action: func(context *cli.Context) error {
		var (
			layerInFd  = syscall.Stdin
			layerOutFd = syscall.Stdout
		)

		decryptData, err := utils.ReadDecryptData()
		if err != nil {
			return errors.Wrapf(err, "could not read config data")
		}

		layerOutFile := os.NewFile(uintptr(layerOutFd), "layerOutFd")
		if layerOutFile == nil {
			return errors.Errorf("layer output file descriptor %d is invalid", layerOutFd)
		}
		layerOutWriter := bufio.NewWriter(layerOutFile)
		defer layerOutFile.Close()

		layerInFile := os.NewFile(uintptr(layerInFd), "layerInFd")
		if layerInFile == nil {
			return errors.Errorf("layer input file descriptor %d is invalid", layerInFd)
		}
		defer layerInFile.Close()

		ltd, err := utils.UnmarshalLayerToolDecryptData(decryptData)
		if err != nil {
			return err
		}
		encryptedLayerReader := bufio.NewReader(layerInFile)

		_, plainLayerReader, _, err := encryption.DecryptLayer(&ltd.DecryptConfig, encryptedLayerReader, ltd.Descriptor, false)
		if err != nil {
			return errors.Wrapf(err, "call to DecryptLayer failed")
		}

		for {
			_, err := io.CopyN(layerOutWriter, plainLayerReader, 10*1024)
			if err != nil {
				if err == io.EOF {
					break
				}
				return errors.Wrapf(err, "could not copy data")
			}
		}
		// do not forget to Flush()!
		layerOutWriter.Flush()
		return nil
	},
}
