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
	"fmt"
	"io/ioutil"
	"syscall"
	"time"

	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	gruntime "github.com/containerd/containerd/runtime/generic"
	ptypes "github.com/gogo/protobuf/types"
	"github.com/pkg/errors"
)

var (
	empty = &ptypes.Empty{}
)

const (
	configFilename = "config.json"
	defaultShim    = "containerd-shim"
)

var _ = (gruntime.PlatformRuntime)(&Runtime{})

// Config options for the runtime
type Config struct {
	// Shim is a path or name of binary implementing the Shim GRPC API
	Shim string `toml:"shim"`
	// Runtime is a path or name of an OCI runtime used by the shim
	Runtime string `toml:"runtime"`
	// RuntimeRoot is the path that shall be used by the OCI runtime for its data
	RuntimeRoot string `toml:"runtime_root"`
	// NoShim calls runc directly from within the pkg
	NoShim bool `toml:"no_shim"`
	// Debug enable debug on the shim
	ShimDebug bool `toml:"shim_debug"`
}

// ID of the runtime
func (r *Runtime) ID() string {
	return pluginID
}

// Delete a task removing all on disk state
func (r *Runtime) Delete(ctx context.Context, c gruntime.Task) (*gruntime.Exit, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	lc, ok := c.(*Task)
	if !ok {
		return nil, fmt.Errorf("task cannot be cast as *Task")
	}
	if err := r.monitor.Stop(lc); err != nil {
		return nil, err
	}
	bundle := r.loadBundle(lc.ID(), namespace)

	rsp, err := lc.shim.Delete(ctx, empty)
	if err != nil {
		if cerr := r.cleanupAfterDeadShim(ctx, bundle, namespace, c.ID(), lc.pid); cerr != nil {
			log.G(ctx).WithError(err).Error("unable to cleanup task")
		}
		return nil, errdefs.FromGRPC(err)
	}
	r.tasks.Delete(ctx, lc.id)
	if err := lc.shim.KillShim(ctx); err != nil {
		log.G(ctx).WithError(err).Error("failed to kill shim")
	}

	if err := bundle.Delete(); err != nil {
		log.G(ctx).WithError(err).Error("failed to delete bundle")
	}
	r.events.Publish(ctx, gruntime.TaskDeleteEventTopic, &eventstypes.TaskDelete{
		ContainerID: lc.id,
		ExitStatus:  rsp.ExitStatus,
		ExitedAt:    rsp.ExitedAt,
		Pid:         rsp.Pid,
	})
	return &gruntime.Exit{
		Status:    rsp.ExitStatus,
		Timestamp: rsp.ExitedAt,
		Pid:       rsp.Pid,
	}, nil
}

// Tasks returns all tasks known to the runtime
func (r *Runtime) Tasks(ctx context.Context) ([]gruntime.Task, error) {
	return r.tasks.GetAll(ctx)
}

func (r *Runtime) restoreTasks(ctx context.Context) ([]*Task, error) {
	dir, err := ioutil.ReadDir(r.state)
	if err != nil {
		return nil, err
	}
	var o []*Task
	for _, namespace := range dir {
		if !namespace.IsDir() {
			continue
		}
		name := namespace.Name()
		log.G(ctx).WithField("namespace", name).Debug("loading tasks in namespace")
		tasks, err := r.loadTasks(ctx, name)
		if err != nil {
			return nil, err
		}
		o = append(o, tasks...)
	}
	return o, nil
}

// Get a specific task by task id
func (r *Runtime) Get(ctx context.Context, id string) (gruntime.Task, error) {
	return r.tasks.Get(ctx, id)
}

func (r *Runtime) cleanupAfterDeadShim(ctx context.Context, bundle *bundle, ns, id string, pid int) error {
	ctx = namespaces.WithNamespace(ctx, ns)
	if err := r.terminate(ctx, bundle, ns, id); err != nil {
		if r.config.ShimDebug {
			return errors.Wrap(err, "failed to terminate task, leaving bundle for debugging")
		}
		log.G(ctx).WithError(err).Warn("failed to terminate task")
	}

	// Notify Client
	exitedAt := time.Now().UTC()
	r.events.Publish(ctx, gruntime.TaskExitEventTopic, &eventstypes.TaskExit{
		ContainerID: id,
		ID:          id,
		Pid:         uint32(pid),
		ExitStatus:  128 + uint32(syscall.Signal(0x9)), // SIGKILL
		ExitedAt:    exitedAt,
	})

	r.tasks.Delete(ctx, id)
	if err := bundle.Delete(); err != nil {
		log.G(ctx).WithError(err).Error("delete bundle")
	}

	r.events.Publish(ctx, gruntime.TaskDeleteEventTopic, &eventstypes.TaskDelete{
		ContainerID: id,
		Pid:         uint32(pid),
		ExitStatus:  128 + uint32(syscall.Signal(0x9)), // SIGKILL
		ExitedAt:    exitedAt,
	})

	return nil
}
