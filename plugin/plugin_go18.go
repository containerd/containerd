// +build go1.8,!windows,amd64

package plugin

import (
	"fmt"
	"path/filepath"
	"plugin"
	"runtime"
)

// loadPlugins loads all plugins for the OS and Arch
// that containerd is built for inside the provided path
func loadPlugins(dir string, onError func(dllPath string, loadErr error) error) error {
	abs, err := filepath.Abs(dir)
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
		if _, loadErr := plugin.Open(lib); loadErr != nil {
			if err := onError(lib, loadErr); err != nil {
				return err
			}
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
