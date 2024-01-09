package integrityverifier

import (
	"github.com/containerd/containerd/v2/pkg/integrity/fsverity"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

// TODO: see if 'root' directory value can be derived from elsewhere
const defaultIntegrityPath = "/var/lib/containerd/io.containerd.content.v1.content/integrity/"

func init() {
	registry.Register(&plugin.Registration{
		Type:   plugins.IntegrityVerifierPlugin,
		ID:     "fsverity",
		Config: &fsverity.Config{StorePath: defaultIntegrityPath},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			cfg := ic.Config.(*fsverity.Config)
			return fsverity.NewValidator(*cfg), nil
		},
	})
}
