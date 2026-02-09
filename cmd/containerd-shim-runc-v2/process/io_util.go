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

package process

import (
	"net/url"
	"os"
	"os/exec"
	"strings"
)

const envPrefix = "env."

// NewBinaryCmd returns a Cmd to be used to start a logging binary.
// The Cmd is generated from the provided uri. Query parameters with the
// "env." prefix are interpreted as environment variables (prefix stripped)
// and appended to the Cmd environment with the container ID and namespace.
// Other query parameters are passed as args.
func NewBinaryCmd(binaryURI *url.URL, id, ns string) *exec.Cmd {
	var args []string
	envs := make(map[string]string)

	for k, vs := range binaryURI.Query() {
		if strings.HasPrefix(k, envPrefix) {
			envKey := strings.TrimPrefix(k, envPrefix)
			if len(vs) > 0 {
				envs[envKey] = vs[0]
			}
		} else {
			args = append(args, k)
			if len(vs) > 0 {
				args = append(args, vs[0])
			}
		}
	}

	cmd := exec.Command(binaryURI.Path, args...)

	cmd.Env = append(cmd.Env,
		"CONTAINER_ID="+id,
		"CONTAINER_NAMESPACE="+ns,
	)

	for k, v := range envs {
		if k == "CONTAINER_ID" || k == "CONTAINER_NAMESPACE" {
			continue
		}
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	return cmd
}

// CloseFiles closes any files passed in.
// It is used for cleanup in the event of unexpected errors.
func CloseFiles(files ...*os.File) {
	for _, file := range files {
		file.Close()
	}
}
