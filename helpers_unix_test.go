// +build !windows

package containerd

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const newLine = "\n"

func withExitStatus(es int) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
		s.Process.Args = []string{"sh", "-c", fmt.Sprintf("exit %d", es)}
		return nil
	}
}

func withProcessArgs(args ...string) oci.SpecOpts {
	return oci.WithProcessArgs(args...)
}

func withCat() oci.SpecOpts {
	return oci.WithProcessArgs("cat")
}

func withTrue() oci.SpecOpts {
	return oci.WithProcessArgs("true")
}

func withExecExitStatus(s *specs.Process, es int) {
	s.Args = []string{"sh", "-c", fmt.Sprintf("exit %d", es)}
}

func withExecArgs(s *specs.Process, args ...string) {
	s.Args = args
}

var (
	withNewSnapshot = WithNewSnapshot
	withImageConfig = oci.WithImageConfig
)
