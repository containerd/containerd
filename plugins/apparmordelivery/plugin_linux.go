//go:build linux

package apparmordelivery

import (
	"github.com/containerd/containerd/v2/pkg/apparmordelivery"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

func init() {
	registry.Register(&plugin.Registration{
		Type:   plugins.ServicePlugin,
		ID:     "apparmordelivery",
		Config: &apparmordelivery.Config{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			cfg := ic.Config.(*apparmordelivery.Config)
			service, err := apparmordelivery.NewService(cfg)
			if err != nil {
				return nil, err
			}
			ic.Meta.Capabilities = []string{"apparmor"}
			return service, nil
		},
	})
}
