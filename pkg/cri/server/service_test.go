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
	"encoding/json"
	"os"
	"testing"

	"github.com/containerd/containerd/oci"
	"github.com/containerd/go-cni"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
	servertesting "github.com/containerd/containerd/pkg/cri/server/testing"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	imagestore "github.com/containerd/containerd/pkg/cri/store/image"
	"github.com/containerd/containerd/pkg/cri/store/label"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	snapshotstore "github.com/containerd/containerd/pkg/cri/store/snapshot"
	ostesting "github.com/containerd/containerd/pkg/os/testing"
	"github.com/containerd/containerd/pkg/registrar"
)

// newTestCRIService creates a fake criService for test.
func newTestCRIService() *criService {
	labels := label.NewStore()
	return &criService{
		config:             testConfig,
		imageFSPath:        testImageFSPath,
		os:                 ostesting.NewFakeOS(),
		sandboxStore:       sandboxstore.NewStore(labels),
		imageStore:         imagestore.NewStore(nil),
		snapshotStore:      snapshotstore.NewStore(),
		sandboxNameIndex:   registrar.NewRegistrar(),
		containerStore:     containerstore.NewStore(labels),
		containerNameIndex: registrar.NewRegistrar(),
		netPlugin: map[string]cni.CNI{
			defaultNetworkPlugin: servertesting.NewFakeCNIPlugin(),
		},
	}
}

func TestLoadBaseOCISpec(t *testing.T) {
	spec := oci.Spec{Version: "1.0.2", Hostname: "default"}

	file, err := os.CreateTemp("", "spec-test-")
	require.NoError(t, err)

	defer func() {
		assert.NoError(t, file.Close())
		assert.NoError(t, os.RemoveAll(file.Name()))
	}()

	err = json.NewEncoder(file).Encode(&spec)
	assert.NoError(t, err)

	config := criconfig.Config{}
	config.Runtimes = map[string]criconfig.Runtime{
		"runc": {BaseRuntimeSpec: file.Name()},
	}

	specs, err := loadBaseOCISpecs(&config)
	assert.NoError(t, err)

	assert.Len(t, specs, 1)

	out, ok := specs[file.Name()]
	assert.True(t, ok, "expected spec with file name %q", file.Name())

	assert.Equal(t, "1.0.2", out.Version)
	assert.Equal(t, "default", out.Hostname)
}
