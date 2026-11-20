//go:build !windows

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

package run

import (
	"context"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/containerd/containerd/v2/cmd/ctr/commands"
)

// TestCPUBurstFlagDefined guards against the cpu-burst flag being read by
// the container opts builder without being defined in ContainerFlags,
// which made `ctr run --cpu-burst` fail with an undefined-flag error.
func TestCPUBurstFlagDefined(t *testing.T) {
	cmd := &cli.Command{
		Name:  "test",
		Flags: commands.ContainerFlags,
		Action: func(context.Context, *cli.Command) error {
			return nil
		},
	}
	if err := cmd.Run(context.Background(), []string{"test", "--cpu-burst", "1000"}); err != nil {
		t.Fatalf("failed to parse --cpu-burst: %v", err)
	}
}

func TestDetectGPUVendor(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name             string
		availableVendors []string
		wantVendor       string
		wantErr          bool
	}{
		{
			name:             "empty list",
			availableVendors: []string{},
			wantErr:          true,
		},
		{
			name:             "nil slice",
			availableVendors: nil,
			wantErr:          true,
		},
		{
			name:             "single vendor nvidia",
			availableVendors: []string{"nvidia.com"},
			wantVendor:       "nvidia.com",
		},
		{
			name:             "single vendor amd",
			availableVendors: []string{"amd.com"},
			wantVendor:       "amd.com",
		},
		{
			name:             "multiple vendors with one known",
			availableVendors: []string{"vendor.com", "nvidia.com", "other.com"},
			wantVendor:       "nvidia.com",
		},
		{
			name:             "multiple known vendors returns nvidia first",
			availableVendors: []string{"amd.com", "nvidia.com"},
			wantVendor:       "nvidia.com",
		},
		{
			name:             "only unknown vendors",
			availableVendors: []string{"other.com", "unknown.com"},
			wantErr:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := detectGPUVendor(ctx, tt.availableVendors)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantVendor {
				t.Errorf("got vendor = %q, want %q", got, tt.wantVendor)
			}
		})
	}
}

func TestGpuDeviceNames(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		vendor      string
		gpuIDs      []int
		wantDevices []string
		wantErr     bool
	}{
		{
			name:        "empty vendor",
			vendor:      "",
			gpuIDs:      []int{0},
			wantDevices: nil,
			wantErr:     true,
		},
		{
			name:        "nil gpu ids",
			vendor:      "vendor.com",
			gpuIDs:      nil,
			wantDevices: nil,
		},
		{
			name:        "empty gpu ids",
			vendor:      "vendor.com",
			gpuIDs:      []int{},
			wantDevices: nil,
		},
		{
			name:        "single gpu id",
			vendor:      "vendor.com",
			gpuIDs:      []int{0},
			wantDevices: []string{"vendor.com/gpu=0"},
		},
		{
			name:        "multiple gpu ids",
			vendor:      "vendor.com",
			gpuIDs:      []int{0, 1, 3},
			wantDevices: []string{"vendor.com/gpu=0", "vendor.com/gpu=1", "vendor.com/gpu=3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gpuDeviceNames(ctx, tt.vendor, tt.gpuIDs...)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.wantDevices) {
				t.Fatalf("got %d devices, want %d", len(got), len(tt.wantDevices))
			}
			for i, device := range got {
				if device != tt.wantDevices[i] {
					t.Errorf("device[%d] = %q, want %q", i, device, tt.wantDevices[i])
				}
			}
		})
	}
}
