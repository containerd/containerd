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

package nri

import (
	"context"
	"time"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
	cstore "github.com/containerd/containerd/pkg/cri/store/container"
	sstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type CRIImplementation interface {
	Config() *criconfig.Config
	SandboxStore() *sstore.Store
	ContainerStore() *cstore.Store
	ContainerMetadataExtensionKey() string
	UpdateContainerResources(context.Context, cstore.Container, *cri.UpdateContainerResourcesRequest, cstore.Status) (cstore.Status, error)
	StopContainer(context.Context, cstore.Container, time.Duration) error
}
