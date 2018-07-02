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
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/events/exchange"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	gruntime "github.com/containerd/containerd/runtime/generic"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

const (
	runtimeName    = "windows"
	defaultRuntime = "runhcs"
)

var (
	pluginID = fmt.Sprintf("%s.%s", plugin.RuntimePlugin, runtimeName)
)

func init() {
	plugin.Register(&plugin.Registration{
		Type:   plugin.RuntimePlugin,
		ID:     runtimeName,
		InitFn: New,
		Requires: []plugin.Type{
			plugin.TaskMonitorPlugin,
			plugin.MetadataPlugin,
		},
		Config: &Config{
			Shim:    defaultShim,
			Runtime: defaultRuntime,
		},
	})
}

// New returns a configured runtime
func New(ic *plugin.InitContext) (interface{}, error) {
	ic.Meta.Platforms = []ocispec.Platform{platforms.DefaultSpec()}

	if err := os.MkdirAll(ic.Root, 0711); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(ic.State, 0711); err != nil {
		return nil, err
	}
	monitor, err := ic.Get(plugin.TaskMonitorPlugin)
	if err != nil {
		return nil, err
	}

	cfg := ic.Config.(*Config)
	r := &Runtime{
		root:    ic.Root,
		state:   ic.State,
		monitor: monitor.(gruntime.TaskMonitor),
		tasks:   gruntime.NewTaskList(),
		address: ic.Address,
		events:  ic.Events,
		config:  cfg,
	}
	tasks, err := r.restoreTasks(ic.Context)
	if err != nil {
		return nil, err
	}

	// TODO: need to add the tasks to the monitor
	for _, t := range tasks {
		if err := r.tasks.AddWithNamespace(t.namespace, t); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Runtime for a windows based system
type Runtime struct {
	root    string
	state   string
	address string

	monitor gruntime.TaskMonitor
	tasks   *gruntime.TaskList
	events  *exchange.Exchange

	config *Config
}

// Create a new task
func (r *Runtime) Create(ctx context.Context, id string, opts gruntime.CreateOpts) (_ gruntime.Task, err error) {
	return nil, errors.New("not implemented")
}

func (r *Runtime) loadBundle(id, namespace string) *bundle {
	return loadBundle(
		id,
		filepath.Join(r.state, namespace, id))
}

func (r *Runtime) loadTasks(ctx context.Context, ns string) ([]*Task, error) {
	return nil, errors.New("not implemented")
}

func (r *Runtime) terminate(ctx context.Context, bundle *bundle, ns, id string) error {
	return errors.New("not implemented")
}
