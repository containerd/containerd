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

package manager

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/dmverity"
)

const (
	// prefixDmverity is the option prefix for dm-verity specific options
	prefixDmverity = "X-containerd.dmverity."
)

// parseDmverityMountOptions extracts dm-verity parameters from mount options
// Returns the root hash, device name, hash offset, and filtered regular options
func parseDmverityMountOptions(mountOptions []string) (string, string, uint64, []string, error) {
	var rootHash string
	var deviceName string
	var hashOffset uint64
	var regularOptions []string

	for _, o := range mountOptions {
		if dmverityOption, isDmverity := strings.CutPrefix(o, prefixDmverity); isDmverity {
			key, value, ok := strings.Cut(dmverityOption, "=")
			if !ok {
				// Ignore unknown boolean flags for forward compatibility
				continue
			}
			switch key {
			case "device-name":
				deviceName = value
			case "roothash":
				rootHash = value
			case "hash-offset":
				var offset uint64
				if _, err := fmt.Sscanf(value, "%d", &offset); err != nil {
					return "", "", 0, nil, fmt.Errorf("invalid hash-offset value %q: %w", value, errdefs.ErrInvalidArgument)
				}
				hashOffset = offset
			default:
				return "", "", 0, nil, fmt.Errorf("unknown dmverity option %q: %w", key, errdefs.ErrInvalidArgument)
			}
		} else {
			regularOptions = append(regularOptions, o)
		}
	}

	return rootHash, deviceName, hashOffset, regularOptions, nil
}

// dmverityTransformer is a mount transformer that sets up dm-verity devices
// for integrity verification. It reads dm-verity options from mount options
// and creates a read-only device-mapper target.
type dmverityTransformer struct{}

func (dmverityTransformer) Transform(ctx context.Context, m mount.Mount, a []mount.ActiveMount) (mount.Mount, error) {
	log.G(ctx).Debugf("transforming dmverity mount: %+v", m)

	supported, err := dmverity.IsSupported()
	if err != nil {
		return mount.Mount{}, fmt.Errorf("dm-verity support check failed: %w", err)
	}
	if !supported {
		return mount.Mount{}, fmt.Errorf("dm-verity is not supported on this system: veritysetup not available or dm_verity module not loaded: %w", errdefs.ErrNotImplemented)
	}

	// Parse dm-verity options from mount options
	rootHash, deviceName, hashOffset, regularOptions, err := parseDmverityMountOptions(m.Options)
	if err != nil {
		return mount.Mount{}, err
	}

	// Generate device name if not specified
	if deviceName == "" {
		deviceName = fmt.Sprintf("dmverity-%d", time.Now().UnixNano())
	}

	// Check if device already exists (for layer reuse)
	devicePath := dmverity.DevicePath(deviceName)
	if _, err := os.Stat(devicePath); err == nil {
		log.G(ctx).WithField("device", devicePath).Debug("dm-verity device already exists, reusing")
		m.Source = devicePath
		m.Options = regularOptions
		return m, nil
	}

	// Create dm-verity device
	log.G(ctx).WithFields(log.Fields{
		"source":      m.Source,
		"device-name": deviceName,
		"hash-offset": hashOffset,
	}).Debug("opening dm-verity device")

	devicePath, err = dmverity.Open(m.Source, deviceName, m.Source, rootHash, hashOffset, nil)
	if err != nil {
		return mount.Mount{}, fmt.Errorf("failed to open dm-verity device: %w", err)
	}

	// Wait for device to appear
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(devicePath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Verify device exists
	if _, err := os.Stat(devicePath); err != nil {
		// Try to close the device we just created
		if closeErr := dmverity.Close(deviceName); closeErr != nil {
			log.G(ctx).WithError(closeErr).Warn("failed to cleanup dm-verity device after creation failure")
		}
		return mount.Mount{}, fmt.Errorf("dm-verity device %q not found after creation: %w", devicePath, err)
	}

	log.G(ctx).WithField("device", devicePath).Info("dm-verity device created successfully")

	// Return updated mount pointing to dm-verity device
	m.Source = devicePath
	m.Options = regularOptions
	return m, nil
}
