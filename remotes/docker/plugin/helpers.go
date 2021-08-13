package plugin

import (
	"context"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/diff/v1"
	imagesapi "github.com/containerd/containerd/api/services/images/v1"
	"github.com/containerd/containerd/api/services/namespaces/v1"
	api "github.com/containerd/containerd/api/services/remotes/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
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

func withLease(ctx context.Context, lm leases.Manager) (context.Context, func(context.Context) error, error) {
	nop := func(context.Context) error { return nil }
	if _, ok := leases.FromContext(ctx); ok {
		return ctx, nop, nil
	}
	l, err := lm.Create(ctx,
		leases.WithRandomID(),
		leases.WithExpiration(24*time.Hour),
	)
	if err != nil {
		return ctx, nop, err
	}
	ctx = leases.WithLease(ctx, l.ID)
	return ctx, func(ctx context.Context) error {
		return lm.Delete(ctx, l)
	}, nil
}

func ociDescriptorToAPI(d v1.Descriptor) types.Descriptor {
	return types.Descriptor{
		MediaType:   d.MediaType,
		Digest:      d.Digest,
		Size_:       d.Size,
		Annotations: d.Annotations,
	}
}

func imageToAPI(i images.Image) api.Image {
	return api.Image{
		Name:      i.Name,
		Labels:    i.Labels,
		Target:    ociDescriptorToAPI(i.Target),
		CreatedAt: i.CreatedAt,
		UpdatedAt: i.UpdatedAt,
	}
}

// getServicesOpts get service options from plugin context.
func getServicesOpts(ic *plugin.InitContext) ([]containerd.ServicesOpt, error) {
	plugins, err := ic.GetByType(plugin.ServicePlugin)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get service plugin")
	}

	ep, err := ic.Get(plugin.EventPlugin)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get event plugin")
	}

	opts := []containerd.ServicesOpt{
		containerd.WithEventService(ep.(containerd.EventService)),
	}
	for s, fn := range map[string]func(interface{}) containerd.ServicesOpt{
		services.ContentService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithContentStore(s.(content.Store))
		},
		services.ImagesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithImageClient(s.(imagesapi.ImagesClient))
		},
		services.SnapshotsService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithSnapshotters(s.(map[string]snapshots.Snapshotter))
		},
		services.DiffService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithDiffClient(s.(diff.DiffClient))
		},
		services.NamespacesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithNamespaceClient(s.(namespaces.NamespacesClient))
		},
		services.LeasesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithLeasesService(s.(leases.Manager))
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
