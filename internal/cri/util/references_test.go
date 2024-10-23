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

	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
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

func TestGetRepoDigestAndTag(t *testing.T) {
	digest := digest.Digest("sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582")
	for _, test := range []struct {
		desc               string
		ref                string
		schema1            bool
		expectedRepoDigest string
		expectedRepoTag    string
	}{
		{
			desc:               "repo tag should be empty if original ref has no tag",
			ref:                "gcr.io/library/busybox@" + digest.String(),
			expectedRepoDigest: "gcr.io/library/busybox@" + digest.String(),
		},
		{
			desc:               "repo tag should not be empty if original ref has tag",
			ref:                "gcr.io/library/busybox:latest",
			expectedRepoDigest: "gcr.io/library/busybox@" + digest.String(),
			expectedRepoTag:    "gcr.io/library/busybox:latest",
		},
		{
			desc:               "repo digest should be empty if original ref is schema1 and has no digest",
			ref:                "gcr.io/library/busybox:latest",
			schema1:            true,
			expectedRepoDigest: "",
			expectedRepoTag:    "gcr.io/library/busybox:latest",
		},
		{
			desc:               "repo digest should not be empty if original ref is schema1 but has digest",
			ref:                "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59594",
			schema1:            true,
			expectedRepoDigest: "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59594",
			expectedRepoTag:    "",
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			named, err := reference.ParseDockerRef(test.ref)
			assert.NoError(t, err)
			repoDigest, repoTag := GetRepoDigestAndTag(named, digest, test.schema1)
			assert.Equal(t, test.expectedRepoDigest, repoDigest)
			assert.Equal(t, test.expectedRepoTag, repoTag)
		})
	}
}
