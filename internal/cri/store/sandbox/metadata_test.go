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

package sandbox

import (
	"encoding/json"
	"testing"

	assertlib "github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestMetadataMarshalUnmarshal(t *testing.T) {
	meta := &Metadata{
		ID:   "test-id",
		Name: "test-name",
		Config: &runtime.PodSandboxConfig{
			Metadata: &runtime.PodSandboxMetadata{
				Name:      "test-name",
				Uid:       "test-uid",
				Namespace: "test-namespace",
				Attempt:   1,
			},
		},
	}
	assert := assertlib.New(t)
	newMeta := &Metadata{}
	newVerMeta := &versionedMetadata{}

	t.Logf("should be able to do json.marshal and produce v2")
	data, err := json.Marshal(meta)
	assert.NoError(err)
	metadataInternal, err := metadataToInternal(*meta, metadataVersion2)
	assert.NoError(err)
	data1, err := json.Marshal(&versionedMetadata{
		Version:  metadataVersion2,
		Metadata: metadataInternal,
	})
	assert.NoError(err)
	assert.EqualExportedValues(data, data1)

	t.Logf("should be able to do MarshalJSON")
	data, err = meta.MarshalJSON()
	assert.NoError(err)
	assert.NoError(newMeta.UnmarshalJSON(data))
	assert.EqualExportedValues(meta, newMeta)

	t.Logf("should be able to do MarshalJSON and json.Unmarshal")
	data, err = meta.MarshalJSON()
	assert.NoError(err)
	assert.NoError(json.Unmarshal(data, newVerMeta))
	v2meta := internalToMetadata(newVerMeta.Metadata, metadataVersion2)
	assert.EqualExportedValues(*meta, v2meta)

	t.Logf("should be able to do json.Marshal and UnmarshalJSON")
	data, err = json.Marshal(meta)
	assert.NoError(err)
	assert.NoError(newMeta.UnmarshalJSON(data))
	assert.EqualExportedValues(meta, newMeta)

	t.Logf("should be able to unmarshal legacy v1 metadata")
	metadataInternal, err = metadataToInternal(*meta, metadataVersion1)
	assert.NoError(err)
	v1data, err := json.Marshal(&versionedMetadata{
		Version:  metadataVersion1,
		Metadata: metadataInternal,
	})
	assert.NoError(err)
	assert.NoError(newMeta.UnmarshalJSON(v1data))
	assert.EqualExportedValues(meta, newMeta)

	t.Logf("should json.Unmarshal fail for unsupported version")
	metadataInternal, err = metadataToInternal(*meta, metadataVersion1)
	assert.NoError(err)
	unsupported, err := json.Marshal(&versionedMetadata{
		Version:  "random-test-version",
		Metadata: metadataInternal,
	})
	assert.NoError(err)
	assert.Error(json.Unmarshal(unsupported, &newMeta))
}
