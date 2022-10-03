//go:build windows
// +build windows

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

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/typeurl"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

// UpdateContainerResources updates ContainerConfig of the container.
// TODO(windows): Figure out whether windows support this.
func (c *criService) UpdateContainerResources(ctx context.Context, r *runtime.UpdateContainerResourcesRequest) (*runtime.UpdateContainerResourcesResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

// updateContainerSpec updates container spec.
// Copied from container_update_resources_linux.go because it only builds on Linux
func updateContainerSpec(ctx context.Context, cntr containerd.Container, spec *runtimespec.Spec) error {
	any, err := typeurl.MarshalAny(spec)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal spec %+v", spec)
	}
	if err := cntr.Update(ctx, func(ctx context.Context, client *containerd.Client, c *containers.Container) error {
		c.Spec = any
		return nil
	}); err != nil {
		return errors.Wrap(err, "failed to update container spec")
	}
	return nil
}
