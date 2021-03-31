// +build darwin

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package client

import (
	"context"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const newLine = "\n"

func withExitStatus(es int) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
		// TODO: use appropriate command instead of dummy (hello) command
		s.Process.Args = []string{"hello"}
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
	return oci.WithProcessArgs("/usr/bin/true")
}

func withExecExitStatus(s *specs.Process, es int) {
	// TODO: use appropriate command instead of dummy (hello) command
	s.Args = []string{"hello"}
}

func withExecArgs(s *specs.Process, args ...string) {
	s.Args = args
}
