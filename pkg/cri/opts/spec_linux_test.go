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

package opts

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeGids(t *testing.T) {
	gids1 := []uint32{3, 2, 1}
	gids2 := []uint32{2, 3, 4}
	assert.Equal(t, []uint32{1, 2, 3, 4}, mergeGids(gids1, gids2))
}

func TestRestrictOOMScoreAdj(t *testing.T) {
	current, err := getCurrentOOMScoreAdj()
	require.NoError(t, err)

	got, err := restrictOOMScoreAdj(current - 1)
	require.NoError(t, err)
	assert.Equal(t, got, current)

	got, err = restrictOOMScoreAdj(current)
	require.NoError(t, err)
	assert.Equal(t, got, current)

	got, err = restrictOOMScoreAdj(current + 1)
	require.NoError(t, err)
	assert.Equal(t, got, current+1)
}
