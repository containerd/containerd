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

package cdi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tags.cncf.io/container-device-interface/pkg/cdi"
)

func TestDetectGPUVendor(t *testing.T) {
	ctx := context.Background()

	const nvidiaSpec = `
cdiVersion: "0.5.0"
kind: "nvidia.com/gpu"
devices:
- name: "0"
  containerEdits:
    env:
    - GPU_0_INJECTED=true
`
	const amdSpec = `
cdiVersion: "0.5.0"
kind: "amd.com/gpu"
devices:
  - name: "0"
    containerEdits:
      env:
        - GPU_0_INJECTED=true
`
	const unknownSpec = `
cdiVersion: "0.5.0"
kind: "vendorX.com/gpu"
devices:
- name: "0"
  containerEdits:
    env:
    - GPU_0_INJECTED=true
`

	tests := []struct {
		name           string
		cdiSpecFiles   map[string]string
		expectError    bool
		expectedVendor string
	}{
		{
			name:         "no CDI specs returns empty vendor",
			cdiSpecFiles: nil,
			expectError:  true,
		},
		{
			name: "NVIDIA GPU vendor detected",
			cdiSpecFiles: map[string]string{
				"nvidia.yaml": nvidiaSpec,
			},
			expectedVendor: "nvidia.com",
		},
		{
			name: "AMD GPU vendor detected",
			cdiSpecFiles: map[string]string{
				"amd.yaml": amdSpec,
			},
			expectedVendor: "amd.com",
		},
		{
			name: "First vendor preferred when multiple present",
			cdiSpecFiles: map[string]string{
				"nvidia.yaml": nvidiaSpec,
				"amd.yaml":    amdSpec,
			},
			expectedVendor: "nvidia.com",
		},
		{
			name: "Unknown vendor returns error",
			cdiSpecFiles: map[string]string{
				"unknown.yaml": unknownSpec,
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cdiDir := t.TempDir()
			for filename, content := range tt.cdiSpecFiles {
				file := filepath.Join(cdiDir, filename)
				err := os.WriteFile(file, []byte(content), 0644)
				require.NoError(t, err)
			}

			err := cdi.Configure(cdi.WithSpecDirs(cdiDir))
			require.NoError(t, err)

			vendor, err := detectGPUVendor(ctx)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedVendor, vendor)
			}
		})
	}
}

func TestGpuDeviceNames(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name            string
		cdiSpecFiles    []string
		gpuIDs          []int
		expectedDevices []string
		expectError     bool
	}{
		{
			name:            "empty GPU IDs returns nil",
			cdiSpecFiles:    nil,
			gpuIDs:          nil,
			expectedDevices: nil,
			expectError:     false,
		},
		{
			name:            "empty GPU IDs slice returns nil",
			cdiSpecFiles:    nil,
			gpuIDs:          []int{},
			expectedDevices: nil,
			expectError:     false,
		},
		{
			name:         "error when no GPU vendor found",
			cdiSpecFiles: nil,
			gpuIDs:       []int{0},
			expectError:  true,
		},
		{
			name: "error when only unknown vendor present",
			cdiSpecFiles: []string{
				`
cdiVersion: "0.5.0"
kind: "vendorX.com/gpu"
devices:
- name: "0"
  containerEdits:
    env:
    - GPU_0_INJECTED=true
`,
			},
			gpuIDs:      []int{0},
			expectError: true,
		},
		{
			name: "single GPU with nvidia vendor",
			cdiSpecFiles: []string{
				`
cdiVersion: "0.5.0"
kind: "nvidia.com/gpu"
devices:
- name: "0"
  containerEdits:
    env:
    - GPU_0_INJECTED=true
`,
			},
			gpuIDs:          []int{0},
			expectedDevices: []string{"nvidia.com/gpu=0"},
			expectError:     false,
		},
		{
			name: "multiple GPUs with nvidia vendor",
			cdiSpecFiles: []string{
				`
cdiVersion: "0.5.0"
kind: "nvidia.com/gpu"
devices:
- name: "0"
  containerEdits:
    env:
    - GPU_0_INJECTED=true
- name: "1"
  containerEdits:
    env:
    - GPU_1_INJECTED=true
`,
			},
			gpuIDs:          []int{0, 1},
			expectedDevices: []string{"nvidia.com/gpu=0", "nvidia.com/gpu=1"},
			expectError:     false,
		},
		{
			name: "single GPU with AMD vendor",
			cdiSpecFiles: []string{
				`
cdiVersion: "0.5.0"
kind: "amd.com/gpu"
devices:
- name: "0"
  containerEdits:
    env:
    - GPU_0_INJECTED=true
`,
			},
			gpuIDs:          []int{0},
			expectedDevices: []string{"amd.com/gpu=0"},
			expectError:     false,
		},
		{
			name: "multiple GPUs with AMD vendor",
			cdiSpecFiles: []string{
				`
cdiVersion: "0.5.0"
kind: "amd.com/gpu"
devices:
- name: "0"
  containerEdits:
    env:
    - GPU_0_INJECTED=true
- name: "1"
  containerEdits:
    env:
    - GPU_1_INJECTED=true
`,
			},
			gpuIDs:          []int{0, 1},
			expectedDevices: []string{"amd.com/gpu=0", "amd.com/gpu=1"},
			expectError:     false,
		},
		{
			name: "non-sequential GPU IDs",
			cdiSpecFiles: []string{
				`
cdiVersion: "0.5.0"
kind: "nvidia.com/gpu"
devices:
- name: "0"
  containerEdits:
    env:
    - GPU_0_INJECTED=true
- name: "2"
  containerEdits:
    env:
    - GPU_2_INJECTED=true
`,
			},
			gpuIDs:          []int{0, 2},
			expectedDevices: []string{"nvidia.com/gpu=0", "nvidia.com/gpu=2"},
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cdiDir := t.TempDir()
			for idx, content := range tt.cdiSpecFiles {
				file := filepath.Join(cdiDir, fmt.Sprintf("spec-%d.yaml", idx))
				err := os.WriteFile(file, []byte(content), 0644)
				require.NoError(t, err)
			}

			err := cdi.Configure(cdi.WithSpecDirs(cdiDir))
			require.NoError(t, err)

			devices, err := gpuDeviceNames(ctx, tt.gpuIDs...)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedDevices, devices)
			}
		})
	}
}
