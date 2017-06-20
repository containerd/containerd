package plugin

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/events"
)

func NewContext(plugins map[PluginType][]interface{}) *InitContext {
	return &InitContext{
		plugins: plugins,
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
