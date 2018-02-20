// +build !windows

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

package oci

import (
	"context"
	"testing"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/namespaces"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestGenerateSpec(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "testing")
	s, err := GenerateSpec(ctx, nil, &containers.Container{ID: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("GenerateSpec() returns a nil spec")
	}

	// check for matching caps
	defaults := defaultCaps()
	for _, cl := range [][]string{
		s.Process.Capabilities.Bounding,
		s.Process.Capabilities.Permitted,
		s.Process.Capabilities.Inheritable,
		s.Process.Capabilities.Effective,
	} {
		for i := 0; i < len(defaults); i++ {
			if cl[i] != defaults[i] {
				t.Errorf("cap at %d does not match set %q != %q", i, defaults[i], cl[i])
			}
		}
	}

	// check default namespaces
	defaultNS := defaultNamespaces()
	for i, ns := range s.Linux.Namespaces {
		if defaultNS[i] != ns {
			t.Errorf("ns at %d does not match set %q != %q", i, defaultNS[i], ns)
		}
	}

	// test that we don't have tty set
	if s.Process.Terminal {
		t.Error("terminal set on default process")
	}
}

func TestSpecWithTTY(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "testing")
	s, err := GenerateSpec(ctx, nil, &containers.Container{ID: t.Name()}, WithTTY)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Process.Terminal {
		t.Error("terminal net set WithTTY()")
	}
	v := s.Process.Env[len(s.Process.Env)-1]
	if v != "TERM=xterm" {
		t.Errorf("xterm not set in env for TTY")
	}
}

func TestWithLinuxNamespace(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "testing")
	replacedNS := specs.LinuxNamespace{Type: specs.NetworkNamespace, Path: "/var/run/netns/test"}

	s, err := GenerateSpec(ctx, nil, &containers.Container{ID: t.Name()}, WithLinuxNamespace(replacedNS))
	if err != nil {
		t.Fatal(err)
	}

	defaultNS := defaultNamespaces()
	found := false
	for i, ns := range s.Linux.Namespaces {
		if ns == replacedNS && !found {
			found = true
			continue
		}
		if defaultNS[i] != ns {
			t.Errorf("ns at %d does not match set %q != %q", i, defaultNS[i], ns)
		}
	}
}
