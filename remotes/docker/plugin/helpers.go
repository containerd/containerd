package plugin

import (
	"github.com/containerd/containerd/api/types"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
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
