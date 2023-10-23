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
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/containerd/containerd/v2/oci"
	"github.com/containerd/go-cni"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	containerstore "github.com/containerd/containerd/v2/pkg/cri/store/container"
	"github.com/containerd/containerd/v2/pkg/cri/store/label"
	sandboxstore "github.com/containerd/containerd/v2/pkg/cri/store/sandbox"
	servertesting "github.com/containerd/containerd/v2/pkg/cri/testing"
	ostesting "github.com/containerd/containerd/v2/pkg/os/testing"
	"github.com/containerd/containerd/v2/pkg/registrar"
	"github.com/containerd/log"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
)

// newTestCRIService creates a fake criService for test.
func newTestCRIService() *criService {
	labels := label.NewStore()
	return &criService{
		imageService:       &fakeImageService{},
		config:             testConfig,
		os:                 ostesting.NewFakeOS(),
		sandboxStore:       sandboxstore.NewStore(labels),
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

func Test_loadBaseOCISpecs(t *testing.T) {
	spec := oci.Spec{
		Version:  "1.0.2",
		Hostname: "default",
		Process: &specs.Process{
			Capabilities: &specs.LinuxCapabilities{
				Inheritable: []string{"CAP_NET_RAW"},
			},
		},
	}
	file, err := os.CreateTemp("", "spec-test-")
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, file.Close())
		assert.NoError(t, os.RemoveAll(file.Name()))
	}()
	err = json.NewEncoder(file).Encode(&spec)
	require.NoError(t, err)
	config := criconfig.Config{}
	config.Runtimes = map[string]criconfig.Runtime{
		"runc": {BaseRuntimeSpec: file.Name()},
	}
	var buffer bytes.Buffer
	logger := &logrus.Logger{
		Out:          &buffer,
		Formatter:    new(logrus.TextFormatter),
		Hooks:        make(logrus.LevelHooks),
		Level:        logrus.InfoLevel,
		ExitFunc:     os.Exit,
		ReportCaller: false,
	}
	log.L = logrus.NewEntry(logger)
	tests := []struct {
		name    string
		args    *criconfig.Config
		message string
	}{
		{
			name:    "args is not nil,print warning",
			args:    &config,
			message: "Provided base runtime spec includes inheritable capabilities, which may be unsafe. See CVE-2022-24769 for more details.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadBaseOCISpecs(tt.args)
			readAll, _ := io.ReadAll(&buffer)
			if tt.message != "" {
				assert.Contains(t, string(readAll), tt.message)
			}
		})
	}
}
