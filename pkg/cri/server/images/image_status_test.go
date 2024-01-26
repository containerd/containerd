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

package images

import (
	"context"
	"testing"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	imagestore "github.com/containerd/containerd/v2/pkg/cri/store/image"
)

func TestImageStatus(t *testing.T) {
	testID := "sha256:d848ce12891bf78792cda4a23c58984033b0c397a55e93a1556202222ecc5ed4" // #nosec G101
	image := imagestore.Image{
		ID:      testID,
		ChainID: "test-chain-id",
		References: []string{
			"gcr.io/library/busybox:latest",
			"gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		},
		Size: 1234,
		ImageSpec: imagespec.Image{
			Config: imagespec.ImageConfig{
				User: "user:group",
			},
		},
	}
	expected := &runtime.Image{
		Id:          testID,
		RepoTags:    []string{"gcr.io/library/busybox:latest"},
		RepoDigests: []string{"gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582"},
		Size_:       uint64(1234),
		Username:    "user",
	}

	c := newTestCRIService()
	t.Logf("should return nil image spec without error for non-exist image")
	resp, err := c.ImageStatus(context.Background(), &runtime.ImageStatusRequest{
		Image: &runtime.ImageSpec{Image: testID},
	})
	assert.NoError(t, err)
	require.NotNil(t, resp)
	assert.Nil(t, resp.GetImage())

	c.imageStore, err = imagestore.NewFakeStore([]imagestore.Image{image})
	assert.NoError(t, err)

	t.Logf("should return correct image status for exist image")
	resp, err = c.ImageStatus(context.Background(), &runtime.ImageStatusRequest{
		Image: &runtime.ImageSpec{Image: testID},
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, expected, resp.GetImage())
}

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

// TestGetUserFromImage tests the logic of getting image uid or user name of image user.
func TestGetUserFromImage(t *testing.T) {
	newI64 := func(i int64) *int64 { return &i }
	for _, test := range []struct {
		desc string
		user string
		uid  *int64
		name string
	}{
		{
			desc: "no gid",
			user: "0",
			uid:  newI64(0),
		},
		{
			desc: "uid/gid",
			user: "0:1",
			uid:  newI64(0),
		},
		{
			desc: "empty user",
			user: "",
		},
		{
			desc: "multiple separators",
			user: "1:2:3",
			uid:  newI64(1),
		},
		{
			desc: "root username",
			user: "root:root",
			name: "root",
		},
		{
			desc: "username",
			user: "test:test",
			name: "test",
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			actualUID, actualName := getUserFromImage(test.user)
			assert.Equal(t, test.uid, actualUID)
			assert.Equal(t, test.name, actualName)
		})
	}
}
