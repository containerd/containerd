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
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	cstore "github.com/containerd/containerd/pkg/cri/store/container"
	sstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
)

type criImplementation struct {
	c *criService
}

func (i *criImplementation) Config() *criconfig.Config {
	return &i.c.config
}

func (i *criImplementation) SandboxStore() *sstore.Store {
	return i.c.sandboxStore
}

func (i *criImplementation) ContainerStore() *cstore.Store {
	return i.c.containerStore
}

func (i *criImplementation) ContainerMetadataExtensionKey() string {
	return containerMetadataExtension
}
