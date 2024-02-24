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

package credential

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/v2/core/credential"
)

const (
	DefaultBinaryPrefix = "/usr/local/bin"
)

type Config struct {
	// CredsStore is suffix of the program you want to use. eg. osxkeychain、secretservice、wincred、pass、file、memory
	CredsStore string `toml:"creds_store"`
}

// NewDefaultConfig By default, containerd looks for the native binary on each of the platforms,
// i.e. "osxkeychain" on macOS, "wincred" on windows, and "pass" on Linux.
// A special case is that on Linux, containerd will fall back to the "secretservice" binary if it cannot find the "pass" binary.
// If none of these binaries are present, it stores the credentials (i.e. password) in base64 encoding in the config files described above.
func NewDefaultConfig() *Config {
	config := defaultConfig()
	_, err := os.Stat(filepath.Join(DefaultBinaryPrefix, fmt.Sprintf("%s%s", credential.DefaultBinaryNamePrefix, config.CredsStore)))
	if err != nil {
		if !os.IsNotExist(err) {
			return &Config{
				CredsStore: credential.FILE.String(),
			}
		}
		_, err = os.Stat(filepath.Join(DefaultBinaryPrefix, fmt.Sprintf("%s%s", credential.DefaultBinaryNamePrefix, credential.SecretService)))
		if err != nil {
			return &Config{
				CredsStore: credential.FILE.String(),
			}
		}
	}
	return &config
}
