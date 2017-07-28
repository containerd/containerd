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

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	imagestore "github.com/kubernetes-incubator/cri-containerd/pkg/store/image"
)

func TestImageStatus(t *testing.T) {
	testID := "sha256:d848ce12891bf78792cda4a23c58984033b0c397a55e93a1556202222ecc5ed4"
	image := imagestore.Image{
		ID:          testID,
		ChainID:     "test-chain-id",
		RepoTags:    []string{"a", "b"},
		RepoDigests: []string{"c", "d"},
		Size:        1234,
		Config: &imagespec.ImageConfig{
			User: "user:group",
		},
	}
	expected := &runtime.Image{
		Id:          testID,
		RepoTags:    []string{"a", "b"},
		RepoDigests: []string{"c", "d"},
		Size_:       uint64(1234),
		Username:    "user",
	}

	c := newTestCRIContainerdService()
	t.Logf("should return nil image spec without error for non-exist image")
	resp, err := c.ImageStatus(context.Background(), &runtime.ImageStatusRequest{
		Image: &runtime.ImageSpec{Image: testID},
	})
	assert.NoError(t, err)
	require.NotNil(t, resp)
	assert.Nil(t, resp.GetImage())

	c.imageStore.Add(image)

	t.Logf("should return correct image status for exist image")
	resp, err = c.ImageStatus(context.Background(), &runtime.ImageStatusRequest{
		Image: &runtime.ImageSpec{Image: testID},
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, expected, resp.GetImage())
}
