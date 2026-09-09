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

// containerd-shim-sandboxed-runc-v2 is an experimental runc shim that
// implements the Sandbox API itself, so that a pod needs no pause container:
// the shim holds the pod namespaces and owns the pod shared files, and runs
// the containers of the pod with the same runc task service as
// containerd-shim-runc-v2. It serves the io.containerd.runc.v2 runtime type;
// a runtime handler selects it with runtime_path and sandboxer = "shim". See
// the sandbox package for the design.
package main

import (
	"context"

	"github.com/containerd/containerd/v2/pkg/shim"
)

func main() {
	shim.RunShim(context.Background(), newManager())
}
