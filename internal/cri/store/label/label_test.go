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

package label

import (
	"testing"

	"github.com/opencontainers/selinux/go-selinux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddThenRemove(t *testing.T) {
	if !selinux.GetEnabled() {
		t.Skip("selinux is not enabled")
	}

	assert := assert.New(t)
	require := require.New(t)
	store := NewStore()
	releaseCount := 0
	reserveCount := 0
	store.Releaser = func(label string) {
		assert.Contains(label, ":c1,c2")
		releaseCount++
		assert.Equal(1, releaseCount)
	}
	store.Reserver = func(label string) {
		assert.Contains(label, ":c1,c2")
		reserveCount++
		assert.Equal(1, reserveCount)
	}

	t.Log("should count to two level")
	require.NoError(store.Reserve("junk:junk:junk:c1,c2"))
	require.NoError(store.Reserve("junk2:junk2:junk2:c1,c2"))

	t.Log("should have one item")
	assert.Len(store.levels, 1)

	t.Log("c1,c2 count should be 2")
	assert.Equal(2, store.levels["c1,c2"])

	store.Release("junk:junk:junk:c1,c2")
	store.Release("junk2:junk2:junk2:c1,c2")

	t.Log("should have 0 items")
	assert.Empty(store.levels)

	t.Log("should have reserved")
	assert.Equal(1, reserveCount)

	t.Log("should have released")
	assert.Equal(1, releaseCount)
}

func TestJunkData(t *testing.T) {
	if !selinux.GetEnabled() {
		t.Skip("selinux is not enabled")
	}

	assert := assert.New(t)
	require := require.New(t)
	store := NewStore()
	releaseCount := 0
	store.Releaser = func(label string) {
		releaseCount++
	}
	reserveCount := 0
	store.Reserver = func(label string) {
		reserveCount++
	}

	t.Log("should ignore empty label")
	require.NoError(store.Reserve(""))
	assert.Empty(store.levels)
	store.Release("")
	assert.Empty(store.levels)
	assert.Equal(0, releaseCount)
	assert.Equal(0, reserveCount)

	t.Log("should fail on bad label")
	require.Error(store.Reserve("junkjunkjunkc1c2"))
	assert.Empty(store.levels)
	store.Release("junkjunkjunkc1c2")
	assert.Empty(store.levels)
	assert.Equal(0, releaseCount)
	assert.Equal(0, reserveCount)

	t.Log("should not release unknown label")
	store.Release("junk2:junk2:junk2:c1,c2")
	assert.Empty(store.levels)
	assert.Equal(0, releaseCount)
	assert.Equal(0, reserveCount)

	t.Log("should release once even if too many deletes")
	require.NoError(store.Reserve("junk2:junk2:junk2:c1,c2"))
	assert.Len(store.levels, 1)
	assert.Equal(1, store.levels["c1,c2"])
	store.Release("junk2:junk2:junk2:c1,c2")
	store.Release("junk2:junk2:junk2:c1,c2")
	assert.Empty(store.levels)
	assert.Equal(1, releaseCount)
	assert.Equal(1, reserveCount)
}
