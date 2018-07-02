// +build windows

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

	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events/exchange"
	gruntime "github.com/containerd/containerd/runtime/generic"
	"github.com/containerd/containerd/runtime/shim/client"
	shim "github.com/containerd/containerd/runtime/shim/v1"
	"github.com/pkg/errors"
)

// Task on a windows based system
type Task struct {
	mu        sync.Mutex
	id        string
	pid       int
	shim      *client.Client
	namespace string
	monitor   gruntime.TaskMonitor
	events    *exchange.Exchange
}

func newTask(id, namespace string, pid int, shim *client.Client, monitor gruntime.TaskMonitor, events *exchange.Exchange) (*Task, error) {
	return &Task{
		id:        id,
		pid:       pid,
		shim:      shim,
		namespace: namespace,
		monitor:   monitor,
		events:    events,
	}, nil
}

// Start the task
func (t *Task) Start(ctx context.Context) error {
	r, err := t.shim.Start(ctx, &shim.StartRequest{
		ID: t.id,
	})
	if err != nil {
		return errdefs.FromGRPC(err)
	}
	t.pid = int(r.Pid)
	if err := t.monitor.Monitor(t); err != nil {
		return err
	}
	t.events.Publish(ctx, gruntime.TaskStartEventTopic, &eventstypes.TaskStart{
		ContainerID: t.id,
		Pid:         uint32(t.pid),
	})
	return nil
}

// Metrics returns runtime specific system level metric information for the task
func (t *Task) Metrics(ctx context.Context) (interface{}, error) {
	return nil, errors.New("not implemented")
}
