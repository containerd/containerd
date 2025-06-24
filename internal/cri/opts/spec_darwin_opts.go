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
	"sync"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/oci"
	osinterface "github.com/containerd/containerd/v2/pkg/os"
)

// WithDarwinMounts adds mounts from CRI's container config + extra mounts.
func WithDarwinMounts(osi osinterface.OS, osManager osinterface.Manager, config *runtime.ContainerConfig, extra []*runtime.Mount) oci.SpecOpts {
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

			var statError error
			mu := osManager.InflightStatsMutex()
			inflightStatMap := osManager.InflightStats()

			mu.Lock()
			inflight, isPending := inflightStatMap[src]
			if isPending {
				// Stat for this path is already in progress. We wait on the
				// condition variable. Wait() atomically unlocks the mutex and
				// suspends the goroutine. When awakened, it re-locks the mutex
				// before returning.
				for !inflight.Done {
					inflight.Cond.Wait()
				}
				statError = inflight.Err
				mu.Unlock()
			} else {
				// There are no active Stats being made to this path. Create a
				// record for other goroutines to find and wait on.
				newInflight := osManager.NewInflightStat()
				newInflight.Cond = sync.NewCond(mu)
				inflightStatMap[src] = newInflight
				mu.Unlock()

				statCtx, statcancel := context.WithTimeout(ctx, osManager.StatTimeout())
				resultChan := make(chan osinterface.StatResult, 1)
				go func() {
					_, err := osi.Stat(src)
					resultChan <- osinterface.StatResult{Err: err}
					close(resultChan)
				}()

				select {
				case res := <-resultChan: // Stat completed
					statError = res.Err
				case <-statCtx.Done(): // timeout / mountpath could be stuck.
					statError = fmt.Errorf("mountpath (%q) could be stuck", src)
				}
				statcancel()

				mu.Lock()
				newInflight.Err = statError
				newInflight.Done = true

				// Delete the src entry from the map so that subsequent requests
				// can still call a new Stat to the src in case the underlying FS
				// has recovered and can serve files.
				delete(inflightStatMap, src)

				newInflight.Cond.Broadcast()
				mu.Unlock()
			}

			// Create the host path if it doesn't exist.
			if statError != nil {
				if !os.IsNotExist(statError) {
					return fmt.Errorf("failed to stat %q: %w", src, statError)
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
