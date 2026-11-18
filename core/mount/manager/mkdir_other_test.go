//go:build !windows

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

package manager

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSplitMkdirPathValue verifies that splitMkdirPathValue splits on
// every colon.
func TestSplitMkdirPathValue(t *testing.T) {
	assert.Equal(t, []string{"/a/b"}, splitMkdirPathValue("/a/b"))
	assert.Equal(t, []string{"/a/b", "700"}, splitMkdirPathValue("/a/b:700"))
	assert.Equal(t, []string{"/a/b", "700", "1000", "1000"}, splitMkdirPathValue("/a/b:700:1000:1000"))
}
