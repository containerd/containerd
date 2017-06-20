package containerd

import specs "github.com/opencontainers/runtime-spec/specs-go"

type SpecOpts func(s *specs.Spec) error

func WithProcessArgs(args ...string) SpecOpts {
	return func(s *specs.Spec) error {
		s.Process.Args = args
		return nil
	}
}

// GenerateSpec will generate a default spec from the provided image
// for use as a containerd container
func GenerateSpec(opts ...SpecOpts) (*specs.Spec, error) {
	s, err := createDefaultSpec()
	if err != nil {
		return nil, err
	}
	for _, o := range opts {
		if err := o(s); err != nil {
			return nil, err
		}
	}
	return s, nil
}
