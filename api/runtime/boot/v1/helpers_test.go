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
)

func TestExtensions(t *testing.T) {
	params := &BootstrapParams{}

	err := params.AddExtension(&RuncV2Extensions{SchedCore: true})
	require.NoError(t, err)

	got, err := GetExtension[*RuncV2Extensions](params)
	require.NoError(t, err)

	assert.True(t, got.SchedCore)
}
func TestExtensionNotFound(t *testing.T) {
	params := &BootstrapParams{}

	_, err := GetExtension[*RuncV2Extensions](params)
	assert.Error(t, err)
}
