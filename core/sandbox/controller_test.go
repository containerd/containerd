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
	"path/filepath"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithRuntimePath(t *testing.T) {
	var opts CreateOptions
	require.NoError(t, WithRuntimePath("")(&opts))
	assert.Empty(t, opts.RuntimePath, "an empty path keeps the runtime name")

	// An absolute path of this platform (a leading slash is not absolute on
	// Windows).
	abs := filepath.Join(t.TempDir(), "containerd-shim-example-v1")
	require.NoError(t, WithRuntimePath(abs)(&opts))
	assert.Equal(t, abs, opts.RuntimePath)

	err := WithRuntimePath("containerd-shim-example-v1")(&opts)
	require.Error(t, err, "a relative path would be taken for a runtime name")
	assert.True(t, errdefs.IsInvalidArgument(err))
}
