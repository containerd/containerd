package plugin

import (
	"fmt"
	"sync"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/snapshot"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

type PluginType int

const (
	RuntimePlugin PluginType = iota + 1
	GRPCPlugin
	SnapshotPlugin
	TaskMonitorPlugin
)

type Registration struct {
	Type   PluginType
	Config interface{}
	Init   func(*InitContext) (interface{}, error)
}

// TODO(@crosbymichael): how do we keep this struct from growing but support dependency injection for loaded plugins?
type InitContext struct {
	Root        string
	State       string
	Runtimes    map[string]Runtime
	Content     content.Store
	Meta        *bolt.DB
	Snapshotter snapshot.Snapshotter
	Config      interface{}
	Context     context.Context
	Monitor     TaskMonitor
}

type Service interface {
	Register(*grpc.Server) error
}

var register = struct {
	sync.Mutex
	r map[string]*Registration
}{
	r: make(map[string]*Registration),
}

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

func Register(name string, r *Registration) error {
	register.Lock()
	defer register.Unlock()
	if _, ok := register.r[name]; ok {
		return fmt.Errorf("plugin already registered as %q", name)
	}
	register.r[name] = r
	return nil
}

func Registrations() map[string]*Registration {
	return register.r
}
