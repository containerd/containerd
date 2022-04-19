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

package sandbox

import (
	"context"

	"github.com/containerd/containerd/api/services/sandbox/v1"
)

// Controller is an interface to manage sandboxes at runtime.
// When running in sandbox mode, shim expected to implement `SandboxService`.
// Shim lifetimes are now managed manually via sandbox API by the containerd's client.
type Controller interface {
	// Start will start new sandbox instance.
	// containerd will run new shim runtime instance and will invoke Start to create a sandbox process.
	// This routine must be invoked before scheduling containers on this instance.
	// Once started clients may run containers via Task service (additionally specifying sandbox id the container will belong to).
	Start(ctx context.Context, sandboxID string) (uint32, error)
	// Shutdown deletes and cleans all tasks and sandbox instance.
	Shutdown(ctx context.Context, sandboxID string) error
	// Wait blocks until sandbox process exits.
	Wait(ctx context.Context, sandboxID string) (*sandbox.ControllerWaitResponse, error)
	// Status will query sandbox process status. It is heavier than Ping call and must be used whenever you need to
	// gather metadata about current sandbox state (status, uptime, resource use, etc).
	Status(ctx context.Context, sandboxID string) (*sandbox.ControllerStatusResponse, error)
}
