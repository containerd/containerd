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

	"github.com/stretchr/testify/assert"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
)

func TestUpdateImageMetadata(t *testing.T) {
	meta := metadata.ImageMetadata{
		ID:      "test-id",
		ChainID: "test-chain-id",
		Size:    1234,
	}
	for desc, test := range map[string]struct {
		repoTags            []string
		repoDigests         []string
		repoTag             string
		repoDigest          string
		expectedRepoTags    []string
		expectedRepoDigests []string
	}{
		"Add duplicated repo tag and digest": {
			repoTags:            []string{"a", "b"},
			repoDigests:         []string{"c", "d"},
			repoTag:             "a",
			repoDigest:          "c",
			expectedRepoTags:    []string{"a", "b"},
			expectedRepoDigests: []string{"c", "d"},
		},
		"Add new repo tag and digest": {
			repoTags:            []string{"a", "b"},
			repoDigests:         []string{"c", "d"},
			repoTag:             "e",
			repoDigest:          "f",
			expectedRepoTags:    []string{"a", "b", "e"},
			expectedRepoDigests: []string{"c", "d", "f"},
		},
		"Add empty repo tag and digest": {
			repoTags:            []string{"a", "b"},
			repoDigests:         []string{"c", "d"},
			repoTag:             "",
			repoDigest:          "",
			expectedRepoTags:    []string{"a", "b"},
			expectedRepoDigests: []string{"c", "d"},
		},
	} {
		t.Logf("TestCase %q", desc)
		m := meta
		m.RepoTags = test.repoTags
		m.RepoDigests = test.repoDigests
		updateImageMetadata(&m, test.repoTag, test.repoDigest)
		assert.Equal(t, test.expectedRepoTags, m.RepoTags)
		assert.Equal(t, test.expectedRepoDigests, m.RepoDigests)
	}
}
