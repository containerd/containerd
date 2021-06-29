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

package tasks

import (
	"context"
	"sync"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/plugin"
	tasksplugin "github.com/containerd/containerd/tasks"
	"github.com/containerd/typeurl"
	"github.com/pkg/errors"
)

func init() {
	const prefix = "types.containerd.io"

	// TaskPlugin is the configuration of a proxy plugin to load. This is sent from the
	// client as types.Any (container.Extension) so register the type_url here.
	typeurl.Register(&tasksplugin.TaskPlugin{}, prefix, "tasks", "TaskPlugin")
}

// taskPluginSet is a plugin collection.
type taskPluginSet struct {
	mu          sync.Mutex
	byTypeAndID map[plugin.Type]map[string]*pluginInfo
}

// pluginInfo is a configuration of a plugin with a finalizer function.
type pluginInfo struct {
	*plugin.Plugin
	finiFn func() error
}

// GetByType returns all plugins with the specific type.
func (p *taskPluginSet) GetByType(t plugin.Type) (map[string]*plugin.Plugin, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	byID, ok := p.byTypeAndID[t]
	if !ok {
		return nil, errors.Wrapf(errdefs.ErrNotFound, "no plugins registered for %s", t)
	}
	plugins := make(map[string]*plugin.Plugin)
	for k, v := range byID {
		plugins[k] = v.Plugin
	}

	return plugins, nil
}

// GetAll returns plugins in the set
func (p *taskPluginSet) GetAll() []*plugin.Plugin {
	p.mu.Lock()
	defer p.mu.Unlock()
	var plugins []*plugin.Plugin
	for _, byID := range p.byTypeAndID {
		for _, pg := range byID {
			plugins = append(plugins, pg.Plugin)
		}
	}
	return plugins
}

// add adds a plugin with a finalizer function.
func (p *taskPluginSet) add(pg *plugin.Plugin, finiFn func() error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byTypeAndID == nil {
		p.byTypeAndID = make(map[plugin.Type]map[string]*pluginInfo)
	}
	if byID, typeok := p.byTypeAndID[pg.Registration.Type]; !typeok {
		p.byTypeAndID[pg.Registration.Type] = map[string]*pluginInfo{
			pg.Registration.ID: {pg, finiFn},
		}
	} else if _, idok := byID[pg.Registration.ID]; !idok {
		byID[pg.Registration.ID] = &pluginInfo{pg, finiFn}
	} else {
		return errors.Wrapf(errdefs.ErrAlreadyExists, "plugin %v already initialized", pg.Registration.URI())
	}
	return nil
}

// remove removes the plugin with calling the finalizer function.
func (p *taskPluginSet) remove(id string, t plugin.Type) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, typeok := p.byTypeAndID[t]; typeok {
		if pg, idok := p.byTypeAndID[t][id]; idok {
			var err error
			if pg.finiFn != nil {
				err = pg.finiFn()
			}
			delete(p.byTypeAndID[t], id)
			return err
		}
	}
	return nil
}

// parseSnapshotterPluginOption parses the snapshotter plugin config appended to the container
// If no plugin is specified, it returns empty strings without error.
//
// This uses container's Extension functionality to enable this feature without changing the
// container API.
func parseSnapshotterPluginOption(ctx context.Context, container *containers.Container) (name, socketPath string, err error) {
	ext, ok := container.Extensions["containerd.io/task/plugin"]
	if !ok {
		return "", "", nil // this is not a plugin. no need to return error.
	}
	data, err := typeurl.UnmarshalAny(&ext)
	if err != nil {
		return "", "", errors.Wrapf(err, "failed to unmarshal metadata extension")
	}
	snSocketInfo := data.(*tasksplugin.TaskPlugin)
	if snSocketInfo.Type != plugin.SnapshotPlugin {
		return "", "", nil // this is not a snapshotter plugin. no need to return error.
	}
	if snSocketInfo.Name == "" || snSocketInfo.Address == "" {
		return "", "", errors.New("snapshotter name and socket path must not empty")
	}
	return snSocketInfo.Name, snSocketInfo.Address, nil
}
