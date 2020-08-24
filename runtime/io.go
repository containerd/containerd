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

package runtime

import (
	"net/url"
	"os"
	"os/exec"
)

type Pipe struct {
	R *os.File
	W *os.File
}

func NewPipe() (*Pipe, error) {
	R, W, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	return &Pipe{
		R: R,
		W: W,
	}, nil
}

func NewBinaryCmd(binaryURI *url.URL, id, ns string) *exec.Cmd {
	var args []string
	for k, vs := range binaryURI.Query() {
		args = append(args, k)
		if len(vs) > 0 {
			args = append(args, vs[0])
		}
	}

	cmd := exec.Command(binaryURI.Path, args...)

	cmd.Env = append(cmd.Env,
		"CONTAINER_ID="+id,
		"CONTAINER_NAMESPACE="+ns,
	)

	return cmd
}
