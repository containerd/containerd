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

package runtime

import (
	"context"
	"time"

	"github.com/containerd/containerd/mount"
	"github.com/gogo/protobuf/types"
)

// IO holds process IO information
type IO struct {
	Stdin    string
	Stdout   string
	Stderr   string
	Terminal bool
}

// CreateOpts contains task creation data
type CreateOpts struct {
	// Spec is the OCI runtime spec
	Spec *types.Any
	// Rootfs mounts to perform to gain access to the container's filesystem
	Rootfs []mount.Mount
	// IO for the container's main process
	IO IO
	// Checkpoint digest to restore container state
	Checkpoint string
	// RuntimeOptions for the runtime
	RuntimeOptions *types.Any
	// TaskOptions received for the task
	TaskOptions *types.Any
	// Runtime to use
	Runtime string
}

// Exit information for a process
type Exit struct {
	Pid       uint32
	Status    uint32
	Timestamp time.Time
}

// PlatformRuntime is responsible for the creation and management of
// tasks and processes for a platform.
type PlatformRuntime interface {
	// ID of the runtime
	ID() string
	// Create creates a task with the provided id and options.
	Create(ctx context.Context, taskID string, opts CreateOpts) (Task, error)
	// Get returns a task.
	Get(ctx context.Context, taskID string) (Task, error)
	// Tasks returns all the current tasks for the runtime.
	// Any container runs at most one task at a time.
	Tasks(ctx context.Context, all bool) ([]Task, error)
	// Add adds a task into runtime.
	Add(ctx context.Context, task Task) error
	// Delete remove a task.
	Delete(ctx context.Context, taskID string)
}

// SandboxOpts describes sandbox creation options
type SandboxOpts struct {
	// RuntimeName is a runtime name containerd should bootstrap
	RuntimeName string
	// RuntimeOpts are options to be provided to shim when bootstrapping
	RuntimeOpts *types.Any
	// Spec is the sandbox spec to use
	Spec *types.Any
}

// SandboxRuntime is an optional extension for PlatformRuntime to manage sandboxes
type SandboxRuntime interface {
	PlatformRuntime

	// Start will launch a new sandbox instance (e.g. shim-binary --start + StartSandbox).
	// This must be called first before using PlatformRuntime routines.
	// Once started, clients may schedule containers on this instance like usual.
	Start(ctx context.Context, sandboxID string, opts SandboxOpts) (Process, error)

	// Shutdown will turn down existing sandbox instance
	Shutdown(ctx context.Context, sandboxID string) error

	// Pause will freeze running sandbox instance
	Pause(ctx context.Context, sandboxID string) error

	// Resume will unfreeze previously paused sandbox instance
	Resume(ctx context.Context, sandboxID string) error

	// Ping is a lightweight shim call to check whether sandbox alive
	Ping(ctx context.Context, sandboxID string) error

	// Status will return current status of the running sandbox instance
	Status(ctx context.Context, sandboxID string) (*types.Any, error)

	// Find will return a sandbox instance by identifier
	Find(ctx context.Context, sandboxID string) (Process, error)
}
