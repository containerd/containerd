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

package server

import (
	"context"
	"testing"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	imagestore "github.com/containerd/containerd/pkg/cri/store/image"
)

func TestImageStatus(t *testing.T) {
	testID := "sha256:d848ce12891bf78792cda4a23c58984033b0c397a55e93a1556202222ecc5ed4"
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
