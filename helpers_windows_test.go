// +build windows

package containerd

import (
	"context"
	"strconv"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const newLine = "\r\n"

func withExitStatus(es int) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
		s.Process.Args = []string{"powershell", "-noprofile", "exit", strconv.Itoa(es)}
		return nil
	}
}

func withProcessArgs(args ...string) oci.SpecOpts {
	return oci.WithProcessArgs(append([]string{"powershell", "-noprofile"}, args...)...)
}

func withCat() oci.SpecOpts {
	return oci.WithProcessArgs("cmd", "/c", "more")
}

func withTrue() oci.SpecOpts {
	return oci.WithProcessArgs("cmd", "/c")
}

func withExecExitStatus(s *specs.Process, es int) {
	s.Args = []string{"powershell", "-noprofile", "exit", strconv.Itoa(es)}
}

func withExecArgs(s *specs.Process, args ...string) {
	s.Args = append([]string{"powershell", "-noprofile"}, args...)
}

func withImageConfig(i Image) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
		s.Windows.LayerFolders = dockerLayerFolders
		return nil
	}
}

func withNewSnapshot(id string, i Image) NewContainerOpts {
	// TODO: when windows has a snapshotter remove the withNewSnapshot helper
	return func(ctx context.Context, client *Client, c *containers.Container) error {
		return nil
	}
}
