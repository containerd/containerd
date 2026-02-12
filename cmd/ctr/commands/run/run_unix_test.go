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
)

// mockVendorLister implements vendorLister for tests.
type mockVendorLister struct {
	vendors []string
}

func (m *mockVendorLister) ListVendors() []string {
	return m.vendors
}

func TestDetectGPUVendor(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		lister     vendorLister
		wantVendor string
		wantErr    bool
	}{
		{
			name:    "nil lister",
			lister:  nil,
			wantErr: true,
		},
		{
			name:    "empty list from lister",
			lister:  &mockVendorLister{vendors: []string{}},
			wantErr: true,
		},
		{
			name:       "single vendor nvidia",
			lister:     &mockVendorLister{vendors: []string{"nvidia.com"}},
			wantVendor: "nvidia.com",
		},
		{
			name:       "single vendor amd",
			lister:     &mockVendorLister{vendors: []string{"amd.com"}},
			wantVendor: "amd.com",
		},
		{
			name:       "multiple vendors with one known",
			lister:     &mockVendorLister{vendors: []string{"vendor.com", "nvidia.com", "other.com"}},
			wantVendor: "nvidia.com",
		},
		{
			name:       "multiple known vendors returns nvidia first",
			lister:     &mockVendorLister{vendors: []string{"amd.com", "nvidia.com"}},
			wantVendor: "nvidia.com",
		},
		{
			name:    "only unknown vendors",
			lister:  &mockVendorLister{vendors: []string{"other.com", "unknown.com"}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := detectGPUVendor(ctx, tt.lister)
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
		lister      vendorLister
		gpuIDs      []int
		wantDevices []string
		wantErr     bool
	}{
		{
			name:    "nil lister",
			lister:  nil,
			gpuIDs:  []int{0},
			wantErr: true,
		},
		{
			name:    "vendor detection fails",
			lister:  &mockVendorLister{vendors: []string{}},
			gpuIDs:  []int{0},
			wantErr: true,
		},
		{
			name:        "empty gpu ids",
			lister:      &mockVendorLister{vendors: []string{"nvidia.com"}},
			gpuIDs:      []int{},
			wantDevices: nil,
		},
		{
			name:        "single gpu id with nvidia",
			lister:      &mockVendorLister{vendors: []string{"nvidia.com"}},
			gpuIDs:      []int{0},
			wantDevices: []string{"nvidia.com/gpu=0"},
		},
		{
			name:        "single gpu id with amd",
			lister:      &mockVendorLister{vendors: []string{"amd.com"}},
			gpuIDs:      []int{2},
			wantDevices: []string{"amd.com/gpu=2"},
		},
		{
			name:        "multiple gpu ids",
			lister:      &mockVendorLister{vendors: []string{"nvidia.com"}},
			gpuIDs:      []int{0, 1, 3},
			wantDevices: []string{"nvidia.com/gpu=0", "nvidia.com/gpu=1", "nvidia.com/gpu=3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gpuDeviceNames(ctx, tt.lister, tt.gpuIDs...)
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
