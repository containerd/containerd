// +build linux

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
	"sync"

	"github.com/containerd/cgroups"
	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events/exchange"
	gruntime "github.com/containerd/containerd/runtime/generic"
	"github.com/containerd/containerd/runtime/shim/client"
	shim "github.com/containerd/containerd/runtime/shim/v1"
	runc "github.com/containerd/go-runc"
	"github.com/pkg/errors"
)

// Task on a linux based system
type Task struct {
	mu        sync.Mutex
	id        string
	pid       int
	shim      *client.Client
	namespace string
	cg        cgroups.Cgroup
	monitor   gruntime.TaskMonitor
	events    *exchange.Exchange
	runtime   *runc.Runc
}

func newTask(id, namespace string, pid int, shim *client.Client, monitor gruntime.TaskMonitor, events *exchange.Exchange, runtime *runc.Runc) (*Task, error) {
	var (
		err error
		cg  cgroups.Cgroup
	)
	if pid > 0 {
		cg, err = cgroups.Load(cgroups.V1, cgroups.PidPath(pid))
		if err != nil && err != cgroups.ErrCgroupDeleted {
			return nil, err
		}
	}
	return &Task{
		id:        id,
		pid:       pid,
		shim:      shim,
		namespace: namespace,
		cg:        cg,
		monitor:   monitor,
		events:    events,
		runtime:   runtime,
	}, nil
}

// Start the task
func (t *Task) Start(ctx context.Context) error {
	t.mu.Lock()
	hasCgroup := t.cg != nil
	t.mu.Unlock()
	r, err := t.shim.Start(ctx, &shim.StartRequest{
		ID: t.id,
	})
	if err != nil {
		return errdefs.FromGRPC(err)
	}
	t.pid = int(r.Pid)
	if !hasCgroup {
		cg, err := cgroups.Load(cgroups.V1, cgroups.PidPath(t.pid))
		if err != nil {
			return err
		}
		t.mu.Lock()
		t.cg = cg
		t.mu.Unlock()
		if err := t.monitor.Monitor(t); err != nil {
			return err
		}
	}
	t.events.Publish(ctx, gruntime.TaskStartEventTopic, &eventstypes.TaskStart{
		ContainerID: t.id,
		Pid:         uint32(t.pid),
	})
	return nil
}

// Metrics returns runtime specific system level metric information for the task
func (t *Task) Metrics(ctx context.Context) (interface{}, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cg == nil {
		return nil, errors.Wrap(errdefs.ErrNotFound, "cgroup does not exist")
	}
	stats, err := t.cg.Stat(cgroups.IgnoreNotExist)
	if err != nil {
		return nil, err
	}
	return stats, nil
}

// Cgroup returns the underlying cgroup for a linux task
func (t *Task) Cgroup() (cgroups.Cgroup, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cg == nil {
		return nil, errors.Wrap(errdefs.ErrNotFound, "cgroup does not exist")
	}
	return t.cg, nil
}
