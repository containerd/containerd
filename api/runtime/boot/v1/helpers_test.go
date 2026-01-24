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

package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestExtensions(t *testing.T) {
	params := &BootstrapParams{}

	err := params.AddExtension(&RuncV2Extensions{SchedCore: true})
	require.NoError(t, err)

	got := &RuncV2Extensions{}
	err = params.FindExtension(got)
	require.NoError(t, err)
	assert.True(t, got.SchedCore)
}

func TestExtensionNotFound(t *testing.T) {
	params := &BootstrapParams{}

	got := &RuncV2Extensions{}
	err := params.FindExtension(got)
	require.NoError(t, err)
}

func TestAddExtensionWithAny(t *testing.T) {
	params := &BootstrapParams{}

	ext := &RuncV2Extensions{SchedCore: true, ShimCgroup: "/test/cgroup"}
	anyVal, err := anypb.New(ext)
	require.NoError(t, err)

	err = params.AddExtension(anyVal)
	require.NoError(t, err)

	got := &RuncV2Extensions{}
	err = params.FindExtension(got)
	require.NoError(t, err)
	assert.True(t, got.SchedCore)
	assert.Equal(t, "/test/cgroup", got.ShimCgroup)

	assert.Contains(t, params.Extensions[0].Value.TypeUrl, "RuncV2Extensions")
}
