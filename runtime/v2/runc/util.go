//go:build linux

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

package runc

import (
	"context"
	"path/filepath"

	"github.com/containerd/containerd/oci"
	"github.com/containerd/log"
	"github.com/opencontainers/runtime-spec/specs-go"
)

// ShouldKillAllOnExit reads the bundle's OCI spec and returns true if
// there is an error reading the spec or if the container has a private PID namespace
func ShouldKillAllOnExit(ctx context.Context, bundlePath string) bool {
	spec, err := oci.ReadSpec(filepath.Join(bundlePath, oci.ConfigFilename))
	if err != nil {
		log.G(ctx).WithError(err).Error("shouldKillAllOnExit: failed to read config.json")
		return true
	}

	if spec.Linux != nil {
		for _, ns := range spec.Linux.Namespaces {
			if ns.Type == specs.PIDNamespace && ns.Path == "" {
				return false
			}
		}
	}
	return true
}
