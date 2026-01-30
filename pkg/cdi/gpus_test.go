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

func writeSpecFiles(t *testing.T, specs []string) string {
	if len(specs) == 0 {
		return t.TempDir()
	}

	dir := t.TempDir()
	for idx, data := range specs {
		file := filepath.Join(dir, fmt.Sprintf("spec-%d.yaml", idx))
		err := os.WriteFile(file, []byte(data), 0644)
		require.NoError(t, err)
	}
	return dir
}

func TestDetectGPUVendor(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		cdiSpecFiles   []string
		expectedVendor string
	}{
		{
			name:           "no CDI specs returns empty vendor",
			cdiSpecFiles:   nil,
			expectedVendor: "",
		},
		{
			name: "nvidia GPU vendor detected",
			cdiSpecFiles: []string{
				`
cdiVersion: "0.5.0"
kind: "nvidia.com/gpu"
devices:
  - name: "0"
    containerEdits:
      deviceNodes:
        - path: /dev/nvidia0
`,
			},
			expectedVendor: vendorNvidia,
		},
		{
			name: "AMD GPU vendor detected",
			cdiSpecFiles: []string{
				`
cdiVersion: "0.5.0"
kind: "amd.com/gpu"
devices:
  - name: "0"
    containerEdits:
      deviceNodes:
        - path: /dev/dri/renderD128
`,
			},
			expectedVendor: vendorAMD,
		},
		{
			name: "nvidia preferred when both vendors present",
			cdiSpecFiles: []string{
				`
cdiVersion: "0.5.0"
kind: "nvidia.com/gpu"
devices:
  - name: "0"
    containerEdits:
      deviceNodes:
        - path: /dev/nvidia0
`,
				`
cdiVersion: "0.5.0"
kind: "amd.com/gpu"
devices:
  - name: "0"
    containerEdits:
      deviceNodes:
        - path: /dev/dri/renderD128
`,
			},
			expectedVendor: vendorNvidia,
		},
		{
			name: "non-GPU vendor ignored",
			cdiSpecFiles: []string{
				`
cdiVersion: "0.5.0"
kind: "example.com/network"
devices:
  - name: "eth0"
    containerEdits:
      env:
        - NETWORK=true
`,
			},
			expectedVendor: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cdiDir := writeSpecFiles(t, tt.cdiSpecFiles)
			err := cdi.Configure(cdi.WithSpecDirs(cdiDir))
			require.NoError(t, err)

			vendor := detectGPUVendor(ctx)
			assert.Equal(t, tt.expectedVendor, vendor)
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
			name: "error when only non-GPU vendor present",
			cdiSpecFiles: []string{
				`
cdiVersion: "0.5.0"
kind: "example.com/network"
devices:
  - name: "eth0"
    containerEdits:
      env:
        - NETWORK=true
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
      deviceNodes:
        - path: /dev/nvidia0
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
      deviceNodes:
        - path: /dev/nvidia0
  - name: "1"
    containerEdits:
      deviceNodes:
        - path: /dev/nvidia1
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
      deviceNodes:
        - path: /dev/dri/renderD128
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
      deviceNodes:
        - path: /dev/dri/renderD128
  - name: "1"
    containerEdits:
      deviceNodes:
        - path: /dev/dri/renderD129
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
      deviceNodes:
        - path: /dev/nvidia0
  - name: "2"
    containerEdits:
      deviceNodes:
        - path: /dev/nvidia2
`,
			},
			gpuIDs:          []int{0, 2},
			expectedDevices: []string{"nvidia.com/gpu=0", "nvidia.com/gpu=2"},
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cdiDir := writeSpecFiles(t, tt.cdiSpecFiles)
			err := cdi.Configure(cdi.WithSpecDirs(cdiDir))
			require.NoError(t, err)

			devices, err := gpuDeviceNames(ctx, tt.gpuIDs...)
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "no GPU vendor found")
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedDevices, devices)
			}
		})
	}
}
