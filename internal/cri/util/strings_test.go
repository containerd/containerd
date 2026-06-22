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

package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInStringSlice(t *testing.T) {
	ss := []string{"ABC", "def", "ghi"}

	assert.True(t, InStringSlice(ss, "ABC"))
	assert.True(t, InStringSlice(ss, "abc"))
	assert.True(t, InStringSlice(ss, "def"))
	assert.True(t, InStringSlice(ss, "DEF"))
	assert.False(t, InStringSlice(ss, "hij"))
	assert.False(t, InStringSlice(ss, "HIJ"))
	assert.False(t, InStringSlice(nil, "HIJ"))
}

func TestSubtractStringSlice(t *testing.T) {
	ss := []string{"ABC", "def", "ghi"}

	assert.Equal(t, []string{"def", "ghi"}, SubtractStringSlice(ss, "abc"))
	assert.Equal(t, []string{"def", "ghi"}, SubtractStringSlice(ss, "ABC"))
	assert.Equal(t, []string{"ABC", "ghi"}, SubtractStringSlice(ss, "def"))
	assert.Equal(t, []string{"ABC", "ghi"}, SubtractStringSlice(ss, "DEF"))
	assert.Equal(t, []string{"ABC", "def", "ghi"}, SubtractStringSlice(ss, "hij"))
	assert.Equal(t, []string{"ABC", "def", "ghi"}, SubtractStringSlice(ss, "HIJ"))
	assert.Empty(t, SubtractStringSlice(nil, "hij"))
	assert.Empty(t, SubtractStringSlice([]string{}, "hij"))
}

func TestMergeStringSlices(t *testing.T) {
	s1 := []string{"abc", "def", "ghi"}
	s2 := []string{"def", "jkl", "mno"}
	expect := []string{"abc", "def", "ghi", "jkl", "mno"}
	result := MergeStringSlices(s1, s2)
	assert.Len(t, result, len(expect))
	for _, s := range expect {
		assert.Contains(t, result, s)
	}
}
