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

// imported from cri-containerd@25fdf726923b607d0155550c5e85a911bb04bad1 (pkg/util/image_test.go)
/*
Copyright 2017 The Kubernetes Authors.

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

package docker1

import (
	"testing"

	"github.com/containerd/containerd/contrib/docker1/reference"
	"github.com/gotestyourself/gotestyourself/assert"
	is "github.com/gotestyourself/gotestyourself/assert/cmp"

	_ "crypto/sha256"
)

func TestNormalizeImageRef(t *testing.T) {
	for _, test := range []struct {
		input        string
		conservative bool
		expect       string
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
			expect: "gcr.io/library/busybox:latest@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		},
		{ // both tag and digest (conservative mode)
			input:        "gcr.io/library/busybox:latest@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
			conservative: true,
			expect:       "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		},
	} {
		t.Logf("TestCase %q", test.input)
		normalized, err := normalizeImageRef(test.input, test.conservative)
		assert.NilError(t, err)
		output := normalized.String()
		assert.Check(t, is.Equal(test.expect, output))
		_, err = reference.Parse(output)
		assert.NilError(t, err, "%q should be containerd supported reference", output)
	}
}
