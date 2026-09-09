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
	"context"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/sandbox"
)

// A failing create option ends Create before any shim is involved: the
// controller has no shim manager here, so getting past the options would panic.
func TestCreateOptionError(t *testing.T) {
	c := &controllerLocal{}
	err := c.Create(context.Background(), sandbox.Sandbox{ID: "sb"}, sandbox.WithRuntimePath("containerd-shim-example-v1"))
	require.ErrorIs(t, err, errdefs.ErrInvalidArgument)
}
