package service

import (
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/images/v1"
	"github.com/containerd/containerd/api/services/namespaces/v1"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/pkg/cri/constants"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/services"
	"github.com/pkg/errors"

	criplatforms "github.com/containerd/containerd/pkg/cri/platforms"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	imagestore "github.com/containerd/containerd/pkg/cri/store/image"
	"github.com/containerd/containerd/pkg/cri/store/label"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
)

// CRIStoreService is the plugin ID
const CRIStoreService = "store"

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.CRIServicePlugin,
		ID:   CRIStoreService,
		Requires: []plugin.Type{
			plugin.ServicePlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			servicesOpts, err := getServicesOpts(ic)
			if err != nil {
				return nil, errors.Wrap(err, "failed to get services")
			}

			client, err := containerd.New(
				"",
				containerd.WithDefaultNamespace(constants.K8sContainerdNamespace),
				containerd.WithDefaultPlatform(criplatforms.Default()),
				containerd.WithServices(servicesOpts...),
			)
			if err != nil {
				return nil, errors.Wrap(err, "failed to create containerd client")
			}
			labels := label.NewStore()
			store := &Store{
				SandboxStore:   sandboxstore.NewStore(labels),
				ContainerStore: containerstore.NewStore(labels),
				ImageStore:     imagestore.NewStore(client),
			}
			return store, nil
		},
	})
}

// getServicesOpts get service options from plugin context.
func getServicesOpts(ic *plugin.InitContext) ([]containerd.ServicesOpt, error) {
	plugins, err := ic.GetByType(plugin.ServicePlugin)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get service plugin")
	}

	opts := []containerd.ServicesOpt{
		containerd.WithEventService(ic.Events),
	}
	for s, fn := range map[string]func(interface{}) containerd.ServicesOpt{
		services.ContentService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithContentStore(s.(content.Store))
		},
		services.ImagesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithImageClient(s.(images.ImagesClient))
		},
		services.NamespacesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithNamespaceClient(s.(namespaces.NamespacesClient))
		},
	} {
		p := plugins[s]
		if p == nil {
			return nil, errors.Errorf("service %q not found", s)
		}
		i, err := p.Instance()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get instance of service %q", s)
		}
		if i == nil {
			return nil, errors.Errorf("instance of service %q not found", s)
		}
		opts = append(opts, fn(i))
	}
	return opts, nil
}

// Store stores all resources associated with cri
type Store struct {
	// sandboxStore stores all resources associated with sandboxes.
	SandboxStore *sandboxstore.Store
	// containerStore stores all resources associated with containers.
	ContainerStore *containerstore.Store
	// imageStore stores all resources associated with images.
	ImageStore *imagestore.Store
}
