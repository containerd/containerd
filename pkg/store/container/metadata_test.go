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

package container

import (
	"encoding/json"
	"testing"

	assertlib "github.com/stretchr/testify/assert"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
)

func TestMetadataEncodeDecode(t *testing.T) {
	meta := &Metadata{
		ID:        "test-id",
		Name:      "test-name",
		SandboxID: "test-sandbox-id",
		Config: &runtime.ContainerConfig{
			Metadata: &runtime.ContainerMetadata{
				Name:    "test-name",
				Attempt: 1,
			},
		},
		ImageRef: "test-image-ref",
	}
	assert := assertlib.New(t)
	data, err := meta.Encode()
	assert.NoError(err)
	newMeta := &Metadata{}
	assert.NoError(newMeta.Decode(data))
	assert.Equal(meta, newMeta)

	unsupported, err := json.Marshal(&versionedMetadata{
		Version:  "random-test-version",
		Metadata: *meta,
	})
	assert.NoError(err)
	assert.Error(newMeta.Decode(unsupported))
}
