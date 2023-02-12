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

package commands

import (
	"context"
	"fmt"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/typeurl/v2"
)

func init() {
	typeurl.Register(&NetworkMetaData{},
		"github.com/containerd/containerd/cmd/ctr/commands", "NetworkMetaData")
}

const (

	// CtrCniMetadataExtension is an extension name that identify metadata of container in CreateContainerRequest
	CtrCniMetadataExtension = "ctr.cni-containerd.metadata"
)

// ctr pass cni network metadata to containerd if ctr run use option of --cni
type NetworkMetaData struct {
	EnableCni bool
}

func FullID(ctx context.Context, c containerd.Container) string {
	id := c.ID()
	ns, ok := namespaces.Namespace(ctx)
	if !ok {
		return id
	}
	return fmt.Sprintf("%s-%s", ns, id)
}
