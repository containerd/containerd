package plugin

import (
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/diff/v1"
	imagesapi "github.com/containerd/containerd/api/services/images/v1"
	"github.com/containerd/containerd/api/services/namespaces/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/services"
	"github.com/containerd/containerd/snapshots"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func platformAPIToOCI(p types.Platform) v1.Platform {
	return v1.Platform{
		Architecture: p.Architecture,
		OS:           p.OS,
		Variant:      p.Variant,
	}
}

func descriptorAPIToOCI(d types.Descriptor) v1.Descriptor {
	return v1.Descriptor{
		MediaType: d.MediaType,
		Digest:    d.Digest,
		Size:      d.Size_,
	}
}

func ociDescriptorToAPI(d v1.Descriptor) types.Descriptor {
	return types.Descriptor{
		MediaType:   d.MediaType,
		Digest:      d.Digest,
		Size_:       d.Size,
		Annotations: d.Annotations,
	}
}

// getServicesOpts get service options from plugin context.
func getServicesOpts(ic *plugin.InitContext) ([]containerd.ServicesOpt, error) {
	cs, err := ic.GetByID(plugin.ServicePlugin, services.ContentService)
	if err != nil {
		return nil, err
	}

	is, err := ic.GetByID(plugin.ServicePlugin, services.ImagesService)
	if err != nil {
		return nil, err
	}

	ss, err := ic.GetByID(plugin.ServicePlugin, services.SnapshotsService)
	if err != nil {
		return nil, err
	}

	ds, err := ic.GetByID(plugin.ServicePlugin, services.DiffService)
	if err != nil {
		return nil, err
	}

	ns, err := ic.GetByID(plugin.ServicePlugin, services.NamespacesService)
	if err != nil {
		return nil, err
	}

	ls, err := ic.GetByID(plugin.ServicePlugin, services.LeasesService)
	if err != nil {
		return nil, err
	}

	ep, err := ic.Get(plugin.EventPlugin)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get event plugin")
	}

	return []containerd.ServicesOpt{
		containerd.WithEventService(ep.(containerd.EventService)),
		containerd.WithContentStore(cs.(content.Store)),
		containerd.WithImageClient(is.(imagesapi.ImagesClient)),
		containerd.WithSnapshotters(ss.(map[string]snapshots.Snapshotter)),
		containerd.WithDiffClient(ds.(diff.DiffClient)),
		containerd.WithNamespaceClient(ns.(namespaces.NamespacesClient)),
		containerd.WithLeasesService(ls.(leases.Manager)),
	}, nil
}
