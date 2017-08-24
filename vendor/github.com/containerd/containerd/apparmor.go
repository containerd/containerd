// +build linux

package containerd

import (
	"context"

	"github.com/containerd/containerd/containers"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// WithApparmor sets the provided apparmor profile to the spec
func WithApparmorProfile(profile string) SpecOpts {
	return func(_ context.Context, _ *Client, _ *containers.Container, s *specs.Spec) error {
		s.Process.ApparmorProfile = profile
		return nil
	}
}
