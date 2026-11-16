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
	"testing"

	"github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestCheckpointContainersProto(t *testing.T) {
	containers := []CheckpointContainer{{ID: "c1", Name: "app"}, {ID: "c2", Name: "sidecar"}}
	assert.Equal(t, containers, CheckpointContainersFromProto(CheckpointContainersToProto(containers)))

	assert.Nil(t, CheckpointContainersToProto(nil))
	assert.Nil(t, CheckpointContainersFromProto(nil))
}

func TestRestoreContainersProto(t *testing.T) {
	spec := &anypb.Any{TypeUrl: "spec"}
	containers := []RestoreContainer{{ID: "c1", Name: "app", Spec: spec}, {ID: "c2", Name: "sidecar"}}

	got := RestoreContainersFromProto(RestoreContainersToProto(containers))
	assert.Equal(t, containers, got)
	assert.Nil(t, got[1].Spec, "a missing spec must stay a nil interface")

	assert.Nil(t, RestoreContainersToProto(nil))
	assert.Nil(t, RestoreContainersFromProto(nil))
}

func TestExtensionsProto(t *testing.T) {
	extensions := map[string]typeurl.Any{"io.example.feature": &anypb.Any{TypeUrl: "feature"}}
	assert.Equal(t, extensions, ExtensionsFromProto(ExtensionsToProto(extensions)))

	assert.Nil(t, ExtensionsToProto(nil))
	assert.Nil(t, ExtensionsFromProto(nil))
}
