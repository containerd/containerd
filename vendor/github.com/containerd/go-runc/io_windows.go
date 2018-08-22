// +build windows

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

package runc

// NewPipeIO creates pipe pairs to be used with runc
func NewPipeIO() (i IO, err error) {
	var pipes []*pipe
	// cleanup in case of an error
	defer func() {
		if err != nil {
			for _, p := range pipes {
				p.Close()
			}
		}
	}()
	stdin, err := newPipe()
	if err != nil {
		return nil, err
	}
	pipes = append(pipes, stdin)

	stdout, err := newPipe()
	if err != nil {
		return nil, err
	}
	pipes = append(pipes, stdout)

	stderr, err := newPipe()
	if err != nil {
		return nil, err
	}
	pipes = append(pipes, stderr)

	return &pipeIO{
		in:  stdin,
		out: stdout,
		err: stderr,
	}, nil
}
