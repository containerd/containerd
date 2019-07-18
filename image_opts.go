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

package containerd

import (
	encconfig "github.com/containerd/containerd/pkg/encryption/config"
)

type imageUnpackOpts struct {
	decryptconfig encconfig.DecryptConfig
}

// ImageOpt allows callers to set options on the containerd
type ImageUnpackOpt func(i *imageUnpackOpts) error

// WithDecryptConfig allows to pass decryption parameters (keys) needed for
// unpacking an encrypted image
func WithDecryptConfig(dc encconfig.DecryptConfig) ImageUnpackOpt {
	return func(i *imageUnpackOpts) error {
		i.decryptconfig = dc
		return nil
	}
}
