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

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/oci"
)

// WithCDIDevices injects the requested CDI devices into the OCI specification.
func WithCDIDevices(devices ...string) oci.SpecOpts {
	return func(ctx context.Context, _ oci.Client, c *containers.Container, s *oci.Spec) error {
		if len(devices) == 0 {
			return nil
		}

		if err := cdi.Refresh(); err != nil {
			// We don't consider registry refresh failure a fatal error.
			// For instance, a dynamically generated invalid CDI Spec file for
			// any particular vendor shouldn't prevent injection of devices of
			// different vendors. CDI itself knows better and it will fail the
			// injection if necessary.
			log.G(ctx).Warnf("CDI registry refresh failed: %v", err)
		}

		if _, err := cdi.InjectDevices(s, devices...); err != nil {
			return fmt.Errorf("CDI device injection failed: %w", err)
		}

		// One crucial thing to keep in mind is that CDI device injection
		// might add OCI Spec environment variables, hooks, and mounts as
		// well. Therefore it is important that none of the corresponding
		// OCI Spec fields are reset up in the call stack once we return.
		return nil
	}
}

// WithGPUs injects CDI GPU devices into the OCI specification.
// It auto-detects the GPU vendor (nvidia.com or amd.com) from available
// CDI specs. If no GPU vendor is found, it returns an error.
//
// The gpuIDs parameter specifies which GPU indices to inject (e.g., 0, 1, 2).
// Each GPU ID is converted to a qualified CDI device name like "vendor.com/gpu=0".
func WithGPUs(gpuIDs ...int) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *oci.Spec) error {
		devices, err := gpuDeviceNames(ctx, gpuIDs...)
		if err != nil {
			return err
		}
		return WithCDIDevices(devices...)(ctx, client, c, s)
	}
}
