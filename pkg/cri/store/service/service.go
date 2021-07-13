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

package service

import (
	"github.com/containerd/containerd/pkg/registrar"

	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	imagestore "github.com/containerd/containerd/pkg/cri/store/image"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
)

// Store stores all resources associated with cri
type Store struct {
	// sandboxStore stores all resources associated with sandboxes.
	SandboxStore *sandboxstore.Store
	// SandboxNameIndex stores all sandbox names and make sure each name
	// is unique.
	SandboxNameIndex *registrar.Registrar
	// containerStore stores all resources associated with containers.
	ContainerStore *containerstore.Store
	// ContainerNameIndex stores all container names and make sure each
	// name is unique.
	ContainerNameIndex *registrar.Registrar
	// imageStore stores all resources associated with images.
	ImageStore *imagestore.Store
}
