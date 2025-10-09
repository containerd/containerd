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

package seccomp

import (
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
)

func TestIOUringIsNotAllowed(t *testing.T) {

	disallowed := map[string]bool{
		"io_uring_enter":    true,
		"io_uring_register": true,
		"io_uring_setup":    true,
	}

	got := DefaultProfile(&specs.Spec{
		Process: &specs.Process{
			Capabilities: &specs.LinuxCapabilities{
				Bounding: []string{},
			},
		},
	})

	for _, config := range got.Syscalls {
		if config.Action != specs.ActAllow {
			continue
		}

		for _, name := range config.Names {
			if disallowed[name] {
				t.Errorf("found disallowed io_uring related syscalls")
			}
		}
	}
}
