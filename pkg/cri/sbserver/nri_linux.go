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

package sbserver

import (
	"context"
	"time"

	cstore "github.com/containerd/containerd/pkg/cri/store/container"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func (i *criImplementation) UpdateContainerResources(ctx context.Context, ctr cstore.Container, req *cri.UpdateContainerResourcesRequest, status cstore.Status) (cstore.Status, error) {
	return i.c.updateContainerResources(ctx, ctr, req, status)
}

func (i *criImplementation) StopContainer(ctx context.Context, ctr cstore.Container, timeout time.Duration) error {
	return i.c.stopContainer(ctx, ctr, timeout)
}
