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

	"github.com/containerd/containerd/reference"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeImageRef(t *testing.T) {
	for _, test := range []struct {
		input  string
		expect string
	}{
		{ // has nothing
			input:  "busybox",
			expect: "docker.io/library/busybox:latest",
		},
		{ // only has tag
			input:  "busybox:latest",
			expect: "docker.io/library/busybox:latest",
		},
		{ // only has digest
			input:  "busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
			expect: "docker.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		},
		{ // only has path
			input:  "library/busybox",
			expect: "docker.io/library/busybox:latest",
		},
		{ // only has hostname
			input:  "docker.io/busybox",
			expect: "docker.io/library/busybox:latest",
		},
		{ // has no tag
			input:  "docker.io/library/busybox",
			expect: "docker.io/library/busybox:latest",
		},
		{ // has no path
			input:  "docker.io/busybox:latest",
			expect: "docker.io/library/busybox:latest",
		},
		{ // has no hostname
			input:  "library/busybox:latest",
			expect: "docker.io/library/busybox:latest",
		},
		{ // full reference
			input:  "docker.io/library/busybox:latest",
			expect: "docker.io/library/busybox:latest",
		},
		{ // gcr reference
			input:  "gcr.io/library/busybox",
			expect: "gcr.io/library/busybox:latest",
		},
		{ // both tag and digest
			input:  "gcr.io/library/busybox:latest@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
			expect: "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		},
	} {
		t.Run(test.input, func(t *testing.T) {
			normalized, err := NormalizeImageRef(test.input)
			assert.NoError(t, err)
			output := normalized.String()
			assert.Equal(t, test.expect, output)
			_, err = reference.Parse(output)
			assert.NoError(t, err, "%q should be containerd supported reference", output)
		})
	}
}
