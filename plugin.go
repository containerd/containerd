package containerd

import (
	"fmt"
	"path/filepath"
	"plugin"
	"runtime"
	"sync"

	"github.com/docker/containerd/content"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

type PluginType int

const (
	RuntimePlugin PluginType = iota + 1
	GRPCPlugin
)

type Registration struct {
	Type   PluginType
	Config interface{}
	Init   func(*InitContext) (interface{}, error)
}

type InitContext struct {
	Root     string
	State    string
	Runtimes map[string]Runtime
	Store    *content.Store
	Config   interface{}
	Context  context.Context
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

// loadPlugins loads all plugins for the OS and Arch
// that containerd is built for inside the provided path
func loadPlugins(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	pattern := filepath.Join(abs, fmt.Sprintf(
		"*-%s-%s.%s",
		runtime.GOOS,
		runtime.GOARCH,
		getLibExt(),
	))
	libs, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, lib := range libs {
		if _, err := plugin.Open(lib); err != nil {
			return err
		}
	}
	return nil
}

// getLibExt returns a platform specific lib extension for
// the platform that containerd is running on
func getLibExt() string {
	switch runtime.GOOS {
	case "windows":
		return "dll"
	default:
		return "so"
	}
}
