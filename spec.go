package containerd

import (
	"context"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/platforms"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

// GenerateSpec will generate a default spec from the provided image
// for use as a containerd container
func GenerateSpec(ctx context.Context, client *Client, c *containers.Container, opts ...SpecOpts) (*specs.Spec, error) {
	return GenerateSpecForPlatform(ctx, client, c, platforms.Default(), opts...)
}

// GenerateSpecForPlatform will generate a default spec for the given OS
// for use as a containerd container
func GenerateSpecForPlatform(ctx context.Context, client *Client, c *containers.Container, platform string, opts ...SpecOpts) (*specs.Spec, error) {
	var (
		s   *specs.Spec
		err error
		os  string
	)
	if platform != "" {
		os, err = platforms.ParseOS(platform)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse provided platform")
		}
	} else {
		os = platforms.DefaultSpec().OS
	}
	switch os {
	case "windows":
		s, err = createDefaultWindowsSpec()
	case "linux":
		s, err = createDefaultLinuxSpec()
	default:
		s, err = createDefaultLinuxSpec()
	}
	if err != nil {
		return nil, err
	}
	for _, o := range opts {
		if err := o(ctx, client, c, s); err != nil {
			return nil, err
		}
	}
	return s, nil
}
