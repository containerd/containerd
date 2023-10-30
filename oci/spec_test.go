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
	"runtime"
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"

	"github.com/containerd/containerd/v2/containers"
	"github.com/containerd/containerd/v2/namespaces"
	"github.com/containerd/containerd/v2/pkg/testutil"
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

	if runtime.GOOS == "linux" {
		// check for matching caps
		defaults := defaultUnixCaps()
		for _, cl := range [][]string{
			s.Process.Capabilities.Bounding,
			s.Process.Capabilities.Permitted,
			s.Process.Capabilities.Effective,
		} {
			for i := 0; i < len(defaults); i++ {
				if cl[i] != defaults[i] {
					t.Errorf("cap at %d does not match set %q != %q", i, defaults[i], cl[i])
				}
			}
		}

		// check default namespaces
		defaultNS := defaultUnixNamespaces()
		for i, ns := range s.Linux.Namespaces {
			if defaultNS[i] != ns {
				t.Errorf("ns at %d does not match set %q != %q", i, defaultNS[i], ns)
			}
		}
	} else if runtime.GOOS == "windows" {
		if s.Windows == nil {
			t.Fatal("Windows section of spec not filled in for Windows spec")
		}
	}

	// test that we don't have tty set
	if s.Process.Terminal {
		t.Error("terminal set on default process")
	}
}

func TestGenerateSpecWithPlatform(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "testing")
	platforms := []string{"windows/amd64", "linux/amd64"}
	for _, p := range platforms {
		t.Logf("Testing platform: %s", p)
		s, err := GenerateSpecWithPlatform(ctx, nil, p, &containers.Container{ID: t.Name()})
		if err != nil {
			t.Fatalf("failed to generate spec: %v", err)
		}

		if s.Root == nil {
			t.Fatal("expected non nil Root section.")
		}
		if s.Process == nil {
			t.Fatal("expected non nil Process section.")
		}
		if p == "windows/amd64" {
			if s.Linux != nil {
				t.Fatal("expected nil Linux section")
			}
			if s.Windows == nil {
				t.Fatal("expected non nil Windows section")
			}
		} else {
			if s.Linux == nil {
				t.Fatal("expected non nil Linux section")
			}
			if runtime.GOOS == "windows" && s.Windows == nil {
				t.Fatal("expected non nil Windows section for LCOW")
			} else if runtime.GOOS != "windows" && s.Windows != nil {
				t.Fatal("expected nil Windows section")
			}
		}
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
	if runtime.GOOS == "linux" {
		v := s.Process.Env[len(s.Process.Env)-1]
		if v != "TERM=xterm" {
			t.Errorf("xterm not set in env for TTY")
		}
	} else {
		if len(s.Process.Env) != 0 {
			t.Fatal("Windows process args should be empty by default")
		}
	}
}

func TestWithLinuxNamespace(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "testing")
	replacedNS := specs.LinuxNamespace{Type: specs.NetworkNamespace, Path: "/var/run/netns/test"}

	var s *specs.Spec
	var err error
	if runtime.GOOS != "windows" {
		s, err = GenerateSpec(ctx, nil, &containers.Container{ID: t.Name()}, WithLinuxNamespace(replacedNS))
	} else {
		s, err = GenerateSpecWithPlatform(ctx, nil, "linux/amd64", &containers.Container{ID: t.Name()}, WithLinuxNamespace(replacedNS))
	}
	if err != nil {
		t.Fatal(err)
	}

	defaultNS := defaultUnixNamespaces()
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

func TestWithCapabilities(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "testing")

	opts := []SpecOpts{
		WithCapabilities([]string{"CAP_SYS_ADMIN"}),
	}
	var s *specs.Spec
	var err error
	if runtime.GOOS != "windows" {
		s, err = GenerateSpec(ctx, nil, &containers.Container{ID: t.Name()}, opts...)
	} else {
		s, err = GenerateSpecWithPlatform(ctx, nil, "linux/amd64", &containers.Container{ID: t.Name()}, opts...)
	}
	if err != nil {
		t.Fatal(err)
	}

	if len(s.Process.Capabilities.Bounding) != 1 || s.Process.Capabilities.Bounding[0] != "CAP_SYS_ADMIN" {
		t.Error("Unexpected capabilities set")
	}
	if len(s.Process.Capabilities.Effective) != 1 || s.Process.Capabilities.Effective[0] != "CAP_SYS_ADMIN" {
		t.Error("Unexpected capabilities set")
	}
	if len(s.Process.Capabilities.Permitted) != 1 || s.Process.Capabilities.Permitted[0] != "CAP_SYS_ADMIN" {
		t.Error("Unexpected capabilities set")
	}
	if len(s.Process.Capabilities.Inheritable) != 0 {
		t.Errorf("Unexpected capabilities set: length is non zero (%d)", len(s.Process.Capabilities.Inheritable))
	}
}

func TestWithCapabilitiesNil(t *testing.T) {
	t.Parallel()

	ctx := namespaces.WithNamespace(context.Background(), "testing")

	s, err := GenerateSpec(ctx, nil, &containers.Container{ID: t.Name()},
		WithCapabilities(nil),
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(s.Process.Capabilities.Bounding) != 0 {
		t.Errorf("Unexpected capabilities set: length is non zero (%d)", len(s.Process.Capabilities.Bounding))
	}
	if len(s.Process.Capabilities.Effective) != 0 {
		t.Errorf("Unexpected capabilities set: length is non zero (%d)", len(s.Process.Capabilities.Effective))
	}
	if len(s.Process.Capabilities.Permitted) != 0 {
		t.Errorf("Unexpected capabilities set: length is non zero (%d)", len(s.Process.Capabilities.Permitted))
	}
	if len(s.Process.Capabilities.Inheritable) != 0 {
		t.Errorf("Unexpected capabilities set: length is non zero (%d)", len(s.Process.Capabilities.Inheritable))
	}
}

func TestPopulateDefaultWindowsSpec(t *testing.T) {
	var (
		c   = containers.Container{ID: "TestWithDefaultSpec"}
		ctx = namespaces.WithNamespace(context.Background(), "test")
	)
	var expected Spec

	populateDefaultWindowsSpec(ctx, &expected, c.ID)
	if expected.Windows == nil {
		t.Error("Cannot populate windows Spec")
	}
}

func TestPopulateDefaultUnixSpec(t *testing.T) {
	var (
		c   = containers.Container{ID: "TestWithDefaultSpec"}
		ctx = namespaces.WithNamespace(context.Background(), "test")
	)
	var expected Spec

	populateDefaultUnixSpec(ctx, &expected, c.ID)
	if expected.Linux == nil {
		t.Error("Cannot populate Unix Spec")
	}
}

func TestWithPrivileged(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "linux" {
		// because WithPrivileged depends on CapEff in /proc/self/status
		testutil.RequiresRoot(t)
	}

	ctx := namespaces.WithNamespace(context.Background(), "testing")

	opts := []SpecOpts{
		WithCapabilities(nil),
		WithMounts([]specs.Mount{
			{Type: "cgroup", Destination: "/sys/fs/cgroup", Options: []string{"ro"}},
		}),
		WithPrivileged,
	}
	var s *specs.Spec
	var err error
	if runtime.GOOS != "windows" {
		s, err = GenerateSpec(ctx, nil, &containers.Container{ID: t.Name()}, opts...)
	} else {
		s, err = GenerateSpecWithPlatform(ctx, nil, "linux/amd64", &containers.Container{ID: t.Name()}, opts...)
	}
	if err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "linux" {
		return
	}

	if len(s.Process.Capabilities.Bounding) == 0 {
		t.Error("Expected capabilities to be set with privileged")
	}

	var foundSys, foundCgroup bool
	for _, m := range s.Mounts {
		switch m.Type {
		case "sysfs":
			foundSys = true
			var found bool
			for _, o := range m.Options {
				switch o {
				case "ro":
					t.Errorf("Found unexpected read only %s mount", m.Type)
				case "rw":
					found = true
				}
			}
			if !found {
				t.Errorf("Did not find rw mount option for %s", m.Type)
			}
		case "cgroup":
			foundCgroup = true
			var found bool
			for _, o := range m.Options {
				switch o {
				case "ro":
					t.Errorf("Found unexpected read only %s mount", m.Type)
				case "rw":
					found = true
				}
			}
			if !found {
				t.Errorf("Did not find rw mount option for %s", m.Type)
			}
		}
	}
	if !foundSys {
		t.Error("Did not find mount for sysfs")
	}
	if !foundCgroup {
		t.Error("Did not find mount for cgroupfs")
	}
}
