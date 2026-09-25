//go:build windows

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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio/pkg/bindfilter"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/log"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// addVolatileOptionOnImageVolumeMount is a no-op on Windows.
func addVolatileOptionOnImageVolumeMount(mounts []mount.Mount) []mount.Mount {
	return mounts
}

// ensureImageVolumeMounted checks whether the target path has an active
// bind filter mount by querying the kernel for active bind filter mappings
// on the volume. This correctly handles the case where containerd restarts
// and the bind filter is removed by the OS while the directory remains on disk.
func ensureImageVolumeMounted(target string) (bool, error) {

	// Normalize path before comparing against bind filter mount points.
	target = filepath.Clean(target)

	// Directory must exist, though its existence alone does not confirm the volume is mounted.
	_, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to stat %s: %w", target, err)
	}

	// Get the volume root for the target path (e.g. "C:\")
	volume := filepath.VolumeName(target) + string(filepath.Separator)

	// Query the kernel for active bind filter mappings on this volume.
	mappings, err := bindfilter.GetBindMappings(volume)
	if err != nil {
		// GetBindMappings can fail if stale bind filter entries reference
		// volumes that are no longer mounted (e.g. after a system restart).
		// Treat this as not mounted so the volume will be remounted.
		log.L.WithError(err).Debugf("failed to get bind mappings for %s, treating as not mounted", volume)
		return false, nil
	}

	// Windows paths are case-insensitive; use EqualFold for comparison.
	for _, m := range mappings {
		if strings.EqualFold(filepath.Clean(m.MountPoint), target) {
			return true, nil
		}
	}
	return false, nil
}

// getImageVolumeSnapshotOpts is a no-op on Windows.
func (c *criService) getImageVolumeSnapshotOpts(ctx context.Context, m *runtime.Mount) ([]snapshots.Opt, error) {
	return nil, nil
}
