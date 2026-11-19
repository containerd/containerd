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

package sandbox

import (
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/internal/cri/store/label"

	assertlib "github.com/stretchr/testify/assert"
)

// TestSandboxStoreProcessLabel verifies that adding a sandbox reserves the MCS level of
// its process label and that deleting it releases the level again. Recovery restores
// Metadata.ProcessLabel and goes through Add, so this is what re-reserves the label of a
// sandbox that survived a containerd restart.
func TestSandboxStoreProcessLabel(t *testing.T) {
	assert := assertlib.New(t)

	const processLabel = "system_u:system_r:container_t:s0:c4,c5"

	s := NewStore(label.NewStore(), nil)
	reserved := map[string]bool{}
	s.labels.Reserver = func(l string) error {
		reserved[strings.SplitN(l, ":", 4)[3]] = true
		return nil
	}
	s.labels.Releaser = func(l string) {
		reserved[strings.SplitN(l, ":", 4)[3]] = false
	}

	sb := NewSandbox(
		Metadata{
			ID:           "1",
			Name:         "Sandbox-1",
			ProcessLabel: processLabel,
		},
		Status{State: StateReady},
	)

	assert.NoError(s.Add(sb))
	assert.True(reserved["s0:c4,c5"], "process label should be reserved on add")

	s.Delete(sb.ID)
	assert.False(reserved["s0:c4,c5"], "process label should be released on delete")
}
