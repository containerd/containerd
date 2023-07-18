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

package mount

import (
	"fmt"
	"os/exec"
)

// Mount to the provided target.
func (m *Mount) mount(target string) error {
	// See https://github.com/slonopotamus/containerd-darwin-mount-helper for reference implementation
	const commandName = "containerd-darwin-mount-helper"

	args := []string{"-t", m.Type}
	for _, option := range m.Options {
		args = append(args, "-o", option)
	}
	args = append(args, m.Source, target)

	cmd := exec.Command(commandName, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s [%v] failed: %q: %w", commandName, args, string(output), err)
	}

	return nil
}
