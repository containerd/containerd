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
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tags.cncf.io/container-device-interface/pkg/cdi"

	"github.com/containerd/containerd/v2/core/containers"
)

func TestWithCDIDevices(t *testing.T) {
	const vendorXSpec = `
cdiVersion: "0.5.0"
kind: "vendorX.com/device"
devices:
- name: "foo"
  containerEdits:
    env:
    - VENDORX_DEVICE_FOO_INJECTED=true
- name: "bar"
  containerEdits:
    env:
    - VENDORX_DEVICE_BAR_INJECTED=true
`

	tests := []struct {
		name         string
		cdiSpecFiles map[string]string
		devices      []string
		expectError  bool
		validateSpec func(t *testing.T, spec *specs.Spec)
	}{
		{
			name:         "Empty devices does nothing",
			cdiSpecFiles: nil,
			devices:      []string{},
			expectError:  false,
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				assert.Empty(t, spec.Process.Env)
			},
		},
		{
			name:         "Nil devices does nothing",
			cdiSpecFiles: nil,
			devices:      nil,
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				assert.Empty(t, spec.Process.Env)
			},
		},
		{
			name: "Single CDI device injection",
			cdiSpecFiles: map[string]string{
				"vendorX.yaml": vendorXSpec,
			},
			devices: []string{"vendorX.com/device=foo"},
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				// Check environment variable was added
				require.NotNil(t, spec.Process)
				require.NotEmpty(t, spec.Process.Env)
				found := false
				for _, env := range spec.Process.Env {
					if env == "VENDORX_DEVICE_FOO_INJECTED=true" {
						found = true
						break
					}
				}
				assert.True(t, found, "environment variable VENDORX_DEVICE_FOO_INJECTED=true not found")
			},
		},
		{
			name: "multiple CDI devices injection",
			cdiSpecFiles: map[string]string{
				"vendorX.yaml": vendorXSpec,
			},
			devices: []string{"vendorX.com/device=foo", "vendorX.com/device=bar"},
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				// Check both environment variables were added
				require.NotNil(t, spec.Process)
				require.NotEmpty(t, spec.Process.Env)

				envVars := make(map[string]bool)
				for _, env := range spec.Process.Env {
					envVars[env] = true
				}
				assert.True(t, envVars["VENDORX_DEVICE_FOO_INJECTED=true"], "env VENDORX_DEVICE_FOO_INJECTED=true not found")
				assert.True(t, envVars["VENDORX_DEVICE_BAR_INJECTED=true"], "env VENDORX_DEVICE_BAR_INJECTED=true not found")
			},
		},
		{
			name: "Error when invalid device with invalid name is injected",
			cdiSpecFiles: map[string]string{
				"vendorX.yaml": vendorXSpec,
			},
			devices:     []string{"vendorX.com/device=99"},
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

			// Create a minimal OCI spec
			spec := &specs.Spec{
				Version: specs.Version,
				Process: &specs.Process{},
				Linux:   &specs.Linux{},
			}
			container := &containers.Container{}

			// Apply the CDI devices option
			opt := WithCDIDevices(tt.devices...)
			err = opt(context.TODO(), nil, container, spec)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "CDI device injection failed")
			} else {
				assert.NoError(t, err)
				tt.validateSpec(t, spec)
			}
		})
	}
}

func TestWithGPUs(t *testing.T) {
	const nvidiaSpec = `
cdiVersion: "0.5.0"
kind: "nvidia.com/gpu"
devices:
- name: "0"
  containerEdits:
    env:
    - NVIDIA_GPU_0_INJECTED=true
- name: "1"
  containerEdits:
    env:
    - NVIDIA_GPU_1_INJECTED=true
- name: "2"
  containerEdits:
    env:
    - NVIDIA_GPU_2_INJECTED=true
`
	const amdSpec = `
cdiVersion: "0.5.0"
kind: "amd.com/gpu"
devices:
- name: "0"
  containerEdits:
    env:
    - AMD_GPU_0_INJECTED=true
- name: "1"
  containerEdits:
    env:
    - AMD_GPU_1_INJECTED=true
`

	tests := []struct {
		name         string
		cdiSpecFiles map[string]string
		gpuIDs       []int
		expectError  bool
		validateSpec func(t *testing.T, spec *specs.Spec)
	}{
		{
			name:         "Empty GPU IDs does nothing",
			cdiSpecFiles: nil,
			gpuIDs:       []int{},
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				assert.Empty(t, spec.Process.Env)
			},
		},
		{
			name:         "Nil GPU IDs does nothing",
			cdiSpecFiles: nil,
			gpuIDs:       nil,
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				assert.Empty(t, spec.Process.Env)
			},
		},
		{
			name:         "Error when no GPU vendor found",
			cdiSpecFiles: nil,
			gpuIDs:       []int{0},
			expectError:  true,
		},
		{
			name: "Successfully inject single NVIDIA GPU",
			cdiSpecFiles: map[string]string{
				"nvidia.yaml": nvidiaSpec,
			},
			gpuIDs: []int{0},
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				require.NotNil(t, spec.Process)
				require.NotEmpty(t, spec.Process.Env)

				envFound := false
				for _, env := range spec.Process.Env {
					if env == "NVIDIA_GPU_0_INJECTED=true" {
						envFound = true
						break
					}
				}
				assert.True(t, envFound, "NVIDIA_GPU_0_INJECTED=true not found")
			},
		},
		{
			name: "Successfully inject multiple NVIDIA GPUs",
			cdiSpecFiles: map[string]string{
				"nvidia.yaml": nvidiaSpec,
			},
			gpuIDs: []int{0, 1},
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				require.NotNil(t, spec.Process)
				require.NotEmpty(t, spec.Process.Env)

				envVars := make(map[string]bool)
				for _, env := range spec.Process.Env {
					envVars[env] = true
				}
				assert.True(t, envVars["NVIDIA_GPU_0_INJECTED=true"], "NVIDIA_GPU_0_INJECTED=true not found")
				assert.True(t, envVars["NVIDIA_GPU_1_INJECTED=true"], "NVIDIA_GPU_1_INJECTED=true not found")
			},
		},
		{
			name: "Successfully inject single AMD GPU",
			cdiSpecFiles: map[string]string{
				"amd.yaml": amdSpec,
			},
			gpuIDs: []int{0},
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				require.NotNil(t, spec.Process)
				require.NotEmpty(t, spec.Process.Env)

				envFound := false
				for _, env := range spec.Process.Env {
					if env == "AMD_GPU_0_INJECTED=true" {
						envFound = true
						break
					}
				}
				assert.True(t, envFound, "AMD_GPU_0_INJECTED=true not found")
			},
		},
		{
			name: "Successfully inject multiple AMD GPUs",
			cdiSpecFiles: map[string]string{
				"amd.yaml": amdSpec,
			},
			gpuIDs: []int{0, 1},
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				require.NotNil(t, spec.Process)
				require.NotEmpty(t, spec.Process.Env)

				envVars := make(map[string]bool)
				for _, env := range spec.Process.Env {
					envVars[env] = true
				}
				assert.True(t, envVars["AMD_GPU_0_INJECTED=true"], "AMD_GPU_0_INJECTED=true not found")
				assert.True(t, envVars["AMD_GPU_1_INJECTED=true"], "AMD_GPU_1_INJECTED=true not found")
			},
		},
		{
			name: "Successfully inject non-sequential GPU IDs",
			cdiSpecFiles: map[string]string{
				"nvidia.yaml": nvidiaSpec,
			},
			gpuIDs: []int{0, 2},
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				require.NotNil(t, spec.Process)
				require.NotEmpty(t, spec.Process.Env)

				envVars := make(map[string]bool)
				for _, env := range spec.Process.Env {
					envVars[env] = true
				}
				assert.True(t, envVars["NVIDIA_GPU_0_INJECTED=true"], "NVIDIA_GPU_0_INJECTED=true not found")
				assert.False(t, envVars["NVIDIA_GPU_1_INJECTED=true"], "NVIDIA_GPU_1_INJECTED=true should not be present")
				assert.True(t, envVars["NVIDIA_GPU_2_INJECTED=true"], "NVIDIA_GPU_2_INJECTED=true not found")
			},
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

			// Create a minimal OCI spec
			spec := &specs.Spec{
				Version: specs.Version,
				Process: &specs.Process{},
				Linux:   &specs.Linux{},
			}
			container := &containers.Container{}

			// Apply the WithGPUs option
			opt := WithGPUs(tt.gpuIDs...)
			err = opt(context.TODO(), nil, container, spec)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				tt.validateSpec(t, spec)
			}
		})
	}
}

func TestWithGPUs_CombinedWithCDIDevices(t *testing.T) {
	const nvidiaSpec = `
cdiVersion: "0.5.0"
kind: "nvidia.com/gpu"
devices:
- name: "0"
  containerEdits:
    env:
    - NVIDIA_GPU_0_INJECTED=true
- name: "1"
  containerEdits:
    env:
    - NVIDIA_GPU_1_INJECTED=true
- name: "2"
  containerEdits:
    env:
    - NVIDIA_GPU_2_INJECTED=true
`
	const amdSpec = `
cdiVersion: "0.5.0"
kind: "amd.com/gpu"
devices:
- name: "0"
  containerEdits:
    env:
    - AMD_GPU_0_INJECTED=true
- name: "1"
  containerEdits:
    env:
    - AMD_GPU_1_INJECTED=true
`
	const vendorXSpec = `
cdiVersion: "0.5.0"
kind: "vendorX.com/device"
devices:
- name: "foo"
  containerEdits:
    env:
    - VENDORX_DEVICE_FOO_INJECTED=true
- name: "bar"
  containerEdits:
    env:
    - VENDORX_DEVICE_BAR_INJECTED=true
`

	tests := []struct {
		name         string
		cdiSpecFiles map[string]string
		gpuIDs       []int
		devices      []string
		validateSpec func(t *testing.T, spec *specs.Spec)
	}{
		{
			name: "Inject different known vendor GPUs via gpu and device options",
			cdiSpecFiles: map[string]string{
				"nvidia.yaml":  nvidiaSpec,
				"vendorx.yaml": vendorXSpec,
			},
			gpuIDs:  []int{1},
			devices: []string{"vendorX.com/device=foo"},
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				require.NotNil(t, spec.Process)
				require.NotEmpty(t, spec.Process.Env)

				envVars := make(map[string]bool)
				for _, env := range spec.Process.Env {
					envVars[env] = true
				}
				assert.True(t, envVars["NVIDIA_GPU_1_INJECTED=true"], "NVIDIA_GPU_1_INJECTED=true not found")
				assert.True(t, envVars["VENDORX_DEVICE_FOO_INJECTED=true"], "VENDORX_DEVICE_FOO_INJECTED=true not found")
			},
		},
		{
			name: "Inject different known vendor GPUs via gpu and device options",
			cdiSpecFiles: map[string]string{
				"nvidia.yaml": nvidiaSpec,
				"amd.yaml":    amdSpec,
			},
			gpuIDs:  []int{2},
			devices: []string{"amd.com/gpu=1"},
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				require.NotNil(t, spec.Process)
				require.NotEmpty(t, spec.Process.Env)

				envVars := make(map[string]bool)
				for _, env := range spec.Process.Env {
					envVars[env] = true
				}
				assert.True(t, envVars["NVIDIA_GPU_2_INJECTED=true"], "NVIDIA_GPU_2_INJECTED=true not found")
				assert.True(t, envVars["AMD_GPU_1_INJECTED=true"], "AMD_GPU_1_INJECTED=true not found")
			},
		},
		{
			name: "Inject devices with gpu and device options for same vendor",
			cdiSpecFiles: map[string]string{
				"nvidia.yaml": nvidiaSpec,
			},
			gpuIDs:  []int{0},
			devices: []string{"nvidia.com/gpu=1"},
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				require.NotNil(t, spec.Process)
				require.NotEmpty(t, spec.Process.Env)

				envVars := make(map[string]bool)
				for _, env := range spec.Process.Env {
					envVars[env] = true
				}
				assert.True(t, envVars["NVIDIA_GPU_0_INJECTED=true"], "NVIDIA_GPU_0_INJECTED=true not found")
				assert.True(t, envVars["NVIDIA_GPU_1_INJECTED=true"], "NVIDIA_GPU_1_INJECTED=true not found")
			},
		},
		{
			name: "Inject same device via gpu and device options",
			cdiSpecFiles: map[string]string{
				"amd.yaml": amdSpec,
			},
			gpuIDs:  []int{1},
			devices: []string{"amd.com/gpu=1"},
			validateSpec: func(t *testing.T, spec *specs.Spec) {
				require.NotNil(t, spec.Process)
				require.NotEmpty(t, spec.Process.Env)

				envVars := make(map[string]bool)
				for _, env := range spec.Process.Env {
					envVars[env] = true
				}
				assert.True(t, envVars["AMD_GPU_1_INJECTED=true"], "AMD_GPU_1_INJECTED=true not found")
			},
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

			// Create a minimal OCI spec
			spec := &specs.Spec{
				Version: specs.Version,
				Process: &specs.Process{},
				Linux:   &specs.Linux{},
			}
			container := &containers.Container{}

			// Apply the WithGPUs option followed by WithCDIDevices
			opt := WithGPUs(tt.gpuIDs...)
			ctx := context.TODO()
			err = opt(ctx, nil, container, spec)
			if err == nil {
				opt2 := WithCDIDevices(tt.devices...)
				err = opt2(ctx, nil, container, spec)
			}

			assert.NoError(t, err)
			tt.validateSpec(t, spec)
		})
	}
}
