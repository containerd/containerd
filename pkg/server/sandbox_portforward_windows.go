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
	"io"

	"github.com/containerd/containerd/errdefs"
	"golang.org/x/net/context"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

// PortForward prepares a streaming endpoint to forward ports from a PodSandbox, and returns the address.
// TODO(windows): Implement this for windows.
func (c *criService) PortForward(ctx context.Context, r *runtime.PortForwardRequest) (*runtime.PortForwardResponse, error) {
	return nil, errdefs.ErrNotImplemented
}

func (c *criService) portForward(ctx context.Context, id string, port int32, stream io.ReadWriter) error {
	return errdefs.ErrNotImplemented
}
