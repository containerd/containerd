package plugin

import (
	"fmt"
	"sync"

	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

var (
	ErrNoPluginType = errors.New("plugin: no type")
	ErrNoPluginID   = errors.New("plugin: no id")

	// SkipPlugin is used when a plugin is not initialized and should not be loaded,
	// this allows the plugin loader differentiate between a plugin which is configured
	// not to load and one that fails to load.
	SkipPlugin = errors.New("skip plugin")
)

// IsSkipPlugin returns true if the error is skipping the plugin
func IsSkipPlugin(err error) bool {
	if errors.Cause(err) == SkipPlugin {
		return true
	}
	return false
}

type PluginType string

const (
	RuntimePlugin     PluginType = "io.containerd.runtime.v1"
	GRPCPlugin        PluginType = "io.containerd.grpc.v1"
	SnapshotPlugin    PluginType = "io.containerd.snapshotter.v1"
	TaskMonitorPlugin PluginType = "io.containerd.monitor.v1"
	DiffPlugin        PluginType = "io.containerd.differ.v1"
	MetadataPlugin    PluginType = "io.containerd.metadata.v1"
	ContentPlugin     PluginType = "io.containerd.content.v1"
)

type Registration struct {
	Type     PluginType
	ID       string
	Config   interface{}
	Requires []PluginType
	Init     func(*InitContext) (interface{}, error)

	added bool
}

func (r *Registration) URI() string {
	return fmt.Sprintf("%s.%s", r.Type, r.ID)
}

type Service interface {
	Register(*grpc.Server) error
}

var register = struct {
	sync.Mutex
	r []*Registration
}{}

// Load loads all plugins at the provided path into containerd
func Load(path string) (err error) {
	defer func() {
		if v := recover(); v != nil {
			rerr, ok := v.(error)
			if !ok {
				rerr = fmt.Errorf("%s", v)
			}
			err = rerr
		}
	}()
	return loadPlugins(path)
}

func Register(r *Registration) {
	register.Lock()
	defer register.Unlock()
	if r.Type == "" {
		panic(ErrNoPluginType)
	}
	if r.ID == "" {
		panic(ErrNoPluginID)
	}
	register.r = append(register.r, r)
}

func Graph() (ordered []*Registration) {
	for _, r := range register.r {
		children(r.Requires, &ordered)
		if !r.added {
			ordered = append(ordered, r)
			r.added = true
		}
	}
	return ordered
}

func children(types []PluginType, ordered *[]*Registration) {
	for _, t := range types {
		for _, r := range register.r {
			if r.Type == t {
				children(r.Requires, ordered)
				if !r.added {
					*ordered = append(*ordered, r)
					r.added = true
				}
			}
		}
	}
}
