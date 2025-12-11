//go:build linux

package seccompdelivery

import (
	"github.com/containerd/containerd/v2/pkg/seccompdelivery"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
)

func init() {
	registry.Register(&plugin.Registration{
		Type:   plugins.ServicePlugin,
		ID:     "seccompdelivery",
		Config: &seccompdelivery.Config{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			cfg := ic.Config.(*seccompdelivery.Config)
			service, err := seccompdelivery.NewService(cfg)
			if err != nil {
				return nil, err
			}
			ic.Meta.Capabilities = []string{"seccomp"}
			return service, nil
		},
	})
}
