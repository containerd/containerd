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

package main

import (
	"context"

	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/manager"
	_ "github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/pause"
	_ "github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/task/plugin"
	"github.com/containerd/containerd/v2/runtime/v2/shim"
)

func main() {
	shim.Run(context.Background(), manager.NewShimManager("io.containerd.runc.v2"))
}
