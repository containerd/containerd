// +build linux

package containerd

import (
	"context"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// WithApparmor sets the provided apparmor profile to the spec
func WithApparmorProfile(profile string) SpecOpts {
	return func(_ context.Context, _ *Client, s *specs.Spec) error {
		s.Process.ApparmorProfile = profile
		return nil
	}
}
