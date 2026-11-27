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

package client_test

import (
	"context"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/transfer/image"
	"github.com/containerd/containerd/v2/core/transfer/registry"
	"github.com/containerd/platforms"
)

func Example_transferPull() {
	var (
		client *containerd.Client
		ctx    context.Context
	)

	ref := "docker.io/library/redis:alpine"
	platform := platforms.DefaultSpec()

	reg, err := registry.NewOCIRegistry(ctx, ref)
	if err != nil {
		return
	}
	is := image.NewStore(ref,
		image.WithPlatforms(platform),
		image.WithUnpack(platform, "overlayfs"),
	)

	if err := client.Transfer(ctx, reg, is); err != nil {
		return
	}

	_, _ = client.GetImage(ctx, ref)
}
