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

package v2

import (
	"testing"

	bootapi "github.com/containerd/containerd/api/runtime/bootstrap/v1"
	"github.com/stretchr/testify/assert"
)

// bootstrappedShimInstance is a ShimInstance that also implements
// shimCapabilities, standing in for a real *shim in tests.
type bootstrappedShimInstance struct {
	ShimInstance
	result *bootapi.BootstrapResult
}

func (s *bootstrappedShimInstance) BootstrapResult() *bootapi.BootstrapResult { return s.result }

func TestSandboxShimExtensions(t *testing.T) {
	extensions := []*bootapi.Extension{{}}

	t.Run("returns the sandbox shim's extensions", func(t *testing.T) {
		process := &bootstrappedShimInstance{result: &bootapi.BootstrapResult{Extensions: extensions}}
		assert.Equal(t, extensions, sandboxShimExtensions(process))
	})

	t.Run("nil bootstrap result returns nil", func(t *testing.T) {
		process := &bootstrappedShimInstance{result: nil}
		assert.Nil(t, sandboxShimExtensions(process))
	})

	t.Run("a shim instance that predates capabilities returns nil", func(t *testing.T) {
		process := &legacyShimInstance{}
		assert.Nil(t, sandboxShimExtensions(process))
	})
}

// legacyShimInstance is a ShimInstance that does not implement
// shimCapabilities, as an out-of-tree implementation predating the
// capability protocol would not.
type legacyShimInstance struct {
	ShimInstance
}
