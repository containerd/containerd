package containerd

import (
	"context"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// GenerateSpec will generate a default spec from the provided image
// for use as a containerd container
func GenerateSpec(ctx context.Context, client *Client, opts ...SpecOpts) (*specs.Spec, error) {
	s, err := createDefaultSpec()
	if err != nil {
		return nil, err
	}
	for _, o := range opts {
		if err := o(ctx, client, s); err != nil {
			return nil, err
		}
	}
	return s, nil
}
