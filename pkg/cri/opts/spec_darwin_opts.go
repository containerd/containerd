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

package opts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	osinterface "github.com/containerd/containerd/pkg/os"
)

// WithDarwinMounts adds mounts from CRI's container config + extra mounts.
func WithDarwinMounts(osi osinterface.OS, config *runtime.ContainerConfig, extra []*runtime.Mount) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, container *containers.Container, s *oci.Spec) error {
		// mergeMounts merge CRI mounts with extra mounts. If a mount destination
		// is mounted by both a CRI mount and an extra mount, the CRI mount will
		// be kept.
		var (
			criMounts = config.GetMounts()
			mounts    = append([]*runtime.Mount{}, criMounts...)
		)

		// Copy all mounts from extra mounts, except for mounts overridden by CRI.
		for _, e := range extra {
			found := false
			for _, c := range criMounts {
				if cleanMount(e.ContainerPath) == cleanMount(c.ContainerPath) {
					found = true
					break
				}
			}
			if !found {
				mounts = append(mounts, e)
			}
		}

		// Sort mounts in number of parts. This ensures that high level mounts don't
		// shadow other mounts.
		sort.Stable(orderedMounts(mounts))

		// Copy all mounts from default mounts, except for
		// - mounts overridden by supplied mount;
		mountSet := make(map[string]struct{})
		for _, m := range mounts {
			mountSet[filepath.Clean(m.ContainerPath)] = struct{}{}
		}

		defaultMounts := s.Mounts
		s.Mounts = nil

		for _, m := range defaultMounts {
			dst := cleanMount(m.Destination)
			if _, ok := mountSet[dst]; ok {
				// filter out mount overridden by a supplied mount
				continue
			}
			s.Mounts = append(s.Mounts, m)
		}

		for _, mount := range mounts {
			var (
				dst = mount.GetContainerPath()
				src = mount.GetHostPath()
			)

			// Create the host path if it doesn't exist.
			if _, err := osi.Stat(src); err != nil {
				if !os.IsNotExist(err) {
					return fmt.Errorf("failed to stat %q: %w", src, err)
				}
				if err := osi.MkdirAll(src, 0755); err != nil {
					return fmt.Errorf("failed to mkdir %q: %w", src, err)
				}
			}

			src, err := osi.ResolveSymbolicLink(src)
			if err != nil {
				return fmt.Errorf("failed to resolve symlink %q: %w", src, err)
			}

			var options []string
			if mount.GetReadonly() {
				options = append(options, "ro")
			} else {
				options = append(options, "rw")
			}

			s.Mounts = append(s.Mounts, runtimespec.Mount{
				Source:      src,
				Destination: dst,
				Type:        "bind",
				Options:     options,
			})
		}
		return nil
	}
}
