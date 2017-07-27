// +build !windows

package containerd

import (
	"context"
	"fmt"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const newLine = "\n"

func generateSpec(opts ...SpecOpts) (*specs.Spec, error) {
	return GenerateSpec(opts...)
}

func withExitStatus(es int) SpecOpts {
	return func(s *specs.Spec) error {
		s.Process.Args = []string{"sh", "-c", fmt.Sprintf("exit %d", es)}
		return nil
	}
}

func withProcessArgs(args ...string) SpecOpts {
	return WithProcessArgs(args...)
}

func withCat() SpecOpts {
	return WithProcessArgs("cat")
}

func withTrue() SpecOpts {
	return WithProcessArgs("true")
}

func withExecExitStatus(s *specs.Process, es int) {
	s.Args = []string{"sh", "-c", fmt.Sprintf("exit %d", es)}
}

func withExecArgs(s *specs.Process, args ...string) {
	s.Args = args
}

func withImageConfig(ctx context.Context, i Image) SpecOpts {
	return WithImageConfig(ctx, i)
}

func withNewSnapshot(id string, i Image) NewContainerOpts {
	return WithNewSnapshot(id, i)
}

var withUserNamespace = WithUserNamespace

var withRemappedSnapshot = WithRemappedSnapshot
