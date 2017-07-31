// +build !go1.8 windows !amd64

package plugin

func loadPlugins(dir string, onError func(dllPath string, openErr error) error) error {
	// plugins not supported until 1.8
	return nil
}
