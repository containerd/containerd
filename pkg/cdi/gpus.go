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

	"github.com/containerd/log"
	"tags.cncf.io/container-device-interface/pkg/cdi"
	"tags.cncf.io/container-device-interface/pkg/parser"
)

const (
	vendorNvidia   = "nvidia.com"
	vendorAMD      = "amd.com"
	deviceClassGPU = "gpu"
)

// knownGPUVendors lists recognized GPU vendors for auto-detection.
var knownGPUVendors = []string{
	vendorNvidia,
	vendorAMD,
}

// detectGPUVendor detects available GPU vendors from CDI spec files.
// It returns the vendor string for the first known GPU vendor found,
// or an empty string if no GPU vendor is detected.
func detectGPUVendor(ctx context.Context) string {
	if err := cdi.Refresh(); err != nil {
		log.G(ctx).Warnf("CDI registry refresh failed during GPU vendor detection: %v", err)
	}

	cache := cdi.GetDefaultCache()
	availableVendors := cache.ListVendors()

	// Check if any known GPU vendor is available
	for _, known := range knownGPUVendors {
		for _, available := range availableVendors {
			if available == known {
				log.G(ctx).Debugf("Detected GPU vendor from CDI specs: %s", known)
				return known
			}
		}
	}

	log.G(ctx).Debugf("No GPU vendor detected from CDI specs")
	return ""
}

// gpuDeviceNames converts GPU indices to qualified CDI device names.
// It auto-detects the GPU vendor (e.g., nvidia.com or amd.com) from available CDI specs.
// Returns the device names and an error if no GPU vendor is found.
func gpuDeviceNames(ctx context.Context, gpuIDs ...int) ([]string, error) {
	if len(gpuIDs) == 0 {
		return nil, nil
	}

	// Detect GPU vendor from CDI specs
	vendor := detectGPUVendor(ctx)
	if vendor == "" {
		return nil, fmt.Errorf("no GPU vendor found in CDI specs; ensure CDI specs are present for your GPU")
	}

	// Build CDI device names from GPU indices
	devices := make([]string, 0, len(gpuIDs))
	for _, id := range gpuIDs {
		devices = append(devices, parser.QualifiedName(vendor, deviceClassGPU, fmt.Sprintf("%d", id)))
	}

	log.G(ctx).Debugf("Resolved GPU device names: %v", devices)
	return devices, nil
}
