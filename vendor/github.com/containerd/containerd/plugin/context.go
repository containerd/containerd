package plugin

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/log"
)

// NewContext returns a new plugin InitContext
func NewContext(ctx context.Context, plugins map[Type]map[string]interface{}, root, state, id string) *InitContext {
	return &InitContext{
		plugins: plugins,
		Root:    filepath.Join(root, id),
		State:   filepath.Join(state, id),
		Context: log.WithModule(ctx, id),
	}
}

// InitContext is used for plugin inititalization
type InitContext struct {
	Root    string
	State   string
	Address string
	Context context.Context
	Config  interface{}
	Events  *events.Exchange

	plugins map[Type]map[string]interface{}
}

// Get returns the first plugin by its type
func (i *InitContext) Get(t Type) (interface{}, error) {
	for _, v := range i.plugins[t] {
		return v, nil
	}
	return nil, fmt.Errorf("no plugins registered for %s", t)
}

// GetAll returns all plugins with the specific type
func (i *InitContext) GetAll(t Type) (map[string]interface{}, error) {
	p, ok := i.plugins[t]
	if !ok {
		return nil, fmt.Errorf("no plugins registered for %s", t)
	}
	return p, nil
}
