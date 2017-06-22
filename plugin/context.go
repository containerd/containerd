package plugin

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/log"
)

func NewContext(ctx context.Context, plugins map[PluginType][]interface{}, root, id string) *InitContext {
	return &InitContext{
		plugins: plugins,
		Root:    filepath.Join(root, id),
		Context: log.WithModule(ctx, id),
	}
}

type InitContext struct {
	Root    string
	Context context.Context
	Config  interface{}
	Emitter *events.Emitter

	plugins map[PluginType][]interface{}
}

func (i *InitContext) Get(t PluginType) (interface{}, error) {
	p := i.plugins[t]
	if len(p) == 0 {
		return nil, fmt.Errorf("no plugins registered for %s", t)
	}
	return p[0], nil
}

func (i *InitContext) GetAll(t PluginType) ([]interface{}, error) {
	p, ok := i.plugins[t]
	if !ok {
		return nil, fmt.Errorf("no plugins registered for %s", t)
	}
	return p, nil
}
