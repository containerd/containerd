package containerd

import specs "github.com/opencontainers/runtime-spec/specs-go"

// SpecOpts sets spec specific information to a newly generated OCI spec
type SpecOpts func(s *specs.Spec) error

// WithProcessArgs replaces the args on the generated spec
func WithProcessArgs(args ...string) SpecOpts {
	return func(s *specs.Spec) error {
		s.Process.Args = args
		return nil
	}
}

// WithHostname sets the container's hostname
func WithHostname(name string) SpecOpts {
	return func(s *specs.Spec) error {
		s.Hostname = name
		return nil
	}
}
