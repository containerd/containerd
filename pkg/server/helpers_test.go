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

package server

import (
	"testing"

	"github.com/containerd/containerd/reference"
	imagedigest "github.com/opencontainers/go-digest"
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
		t.Logf("TestCase %q", test.input)
		normalized, err := normalizeImageRef(test.input)
		assert.NoError(t, err)
		output := normalized.String()
		assert.Equal(t, test.expect, output)
		_, err = reference.Parse(output)
		assert.NoError(t, err, "%q should be containerd supported reference", output)
	}
}

// TestGetUserFromImage tests the logic of getting image uid or user name of image user.
func TestGetUserFromImage(t *testing.T) {
	newI64 := func(i int64) *int64 { return &i }
	for c, test := range map[string]struct {
		user string
		uid  *int64
		name string
	}{
		"no gid": {
			user: "0",
			uid:  newI64(0),
		},
		"uid/gid": {
			user: "0:1",
			uid:  newI64(0),
		},
		"empty user": {
			user: "",
		},
		"multiple spearators": {
			user: "1:2:3",
			uid:  newI64(1),
		},
		"root username": {
			user: "root:root",
			name: "root",
		},
		"username": {
			user: "test:test",
			name: "test",
		},
	} {
		t.Logf("TestCase - %q", c)
		actualUID, actualName := getUserFromImage(test.user)
		assert.Equal(t, test.uid, actualUID)
		assert.Equal(t, test.name, actualName)
	}
}

func TestGetRepoDigestAndTag(t *testing.T) {
	digest := imagedigest.Digest("sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582")
	for desc, test := range map[string]struct {
		ref                string
		schema1            bool
		expectedRepoDigest string
		expectedRepoTag    string
	}{
		"repo tag should be empty if original ref has no tag": {
			ref:                "gcr.io/library/busybox@" + digest.String(),
			expectedRepoDigest: "gcr.io/library/busybox@" + digest.String(),
		},
		"repo tag should not be empty if original ref has tag": {
			ref:                "gcr.io/library/busybox:latest",
			expectedRepoDigest: "gcr.io/library/busybox@" + digest.String(),
			expectedRepoTag:    "gcr.io/library/busybox:latest",
		},
		"repo digest should be empty if original ref is schema1 and has no digest": {
			ref:                "gcr.io/library/busybox:latest",
			schema1:            true,
			expectedRepoDigest: "",
			expectedRepoTag:    "gcr.io/library/busybox:latest",
		},
		"repo digest should not be empty if orignal ref is schema1 but has digest": {
			ref:                "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59594",
			schema1:            true,
			expectedRepoDigest: "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59594",
			expectedRepoTag:    "",
		},
	} {
		t.Logf("TestCase %q", desc)
		named, err := normalizeImageRef(test.ref)
		assert.NoError(t, err)
		repoDigest, repoTag := getRepoDigestAndTag(named, digest, test.schema1)
		assert.Equal(t, test.expectedRepoDigest, repoDigest)
		assert.Equal(t, test.expectedRepoTag, repoTag)
	}
}
