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

func TestParseImageReferences(t *testing.T) {
	refs := []string{
		"gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		"gcr.io/library/busybox:1.2",
		"sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		"arbitrary-ref",
	}
	expectedTags := []string{
		"gcr.io/library/busybox:1.2",
	}
	expectedDigests := []string{"gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582"}
	tags, digests := ParseImageReferences(refs)
	assert.Equal(t, expectedTags, tags)
	assert.Equal(t, expectedDigests, digests)
}
