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
	"github.com/stretchr/testify/require"
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
	require := require.New(t)
	newMeta := &Metadata{}
	newVerMeta := &versionedMetadata{}

	t.Logf("should be able to do json.marshal")
	data, err := json.Marshal(meta)
	require.NoError(err)
	data1, err := json.Marshal(&versionedMetadata{
		Version:  metadataVersion,
		Metadata: metadataInternal(*meta),
	})
	require.NoError(err)
	assert.Equal(data, data1)

	t.Logf("should be able to do MarshalJSON")
	data, err = meta.MarshalJSON()
	require.NoError(err)
	require.NoError(newMeta.UnmarshalJSON(data))
	assert.Equal(meta, newMeta)

	t.Logf("should be able to do MarshalJSON and json.Unmarshal")
	data, err = meta.MarshalJSON()
	require.NoError(err)
	require.NoError(json.Unmarshal(data, newVerMeta))
	assert.Equal((*Metadata)(&newVerMeta.Metadata), meta)

	t.Logf("should be able to do json.Marshal and UnmarshalJSON")
	data, err = json.Marshal(meta)
	require.NoError(err)
	require.NoError(newMeta.UnmarshalJSON(data))
	assert.Equal(meta, newMeta)

	t.Logf("should json.Unmarshal fail for unsupported version")
	unsupported, err := json.Marshal(&versionedMetadata{
		Version:  "random-test-version",
		Metadata: metadataInternal(*meta),
	})
	require.NoError(err)
	require.Error(json.Unmarshal(unsupported, &newMeta))
}
