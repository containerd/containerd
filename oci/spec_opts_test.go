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
	"encoding/json"
	"io/ioutil"
	"log"
	"os"
	"reflect"
	"runtime"
	"testing"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/namespaces"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestWithEnv(t *testing.T) {
	t.Parallel()

	s := Spec{}
	s.Process = &specs.Process{
		Env: []string{"DEFAULT=test"},
	}

	WithEnv([]string{"env=1"})(nil, nil, nil, &s)

	if len(s.Process.Env) != 2 {
		t.Fatal("didn't append")
	}

	WithEnv([]string{"env2=1"})(nil, nil, nil, &s)

	if len(s.Process.Env) != 3 {
		t.Fatal("didn't append")
	}

	WithEnv([]string{"env2=2"})(nil, nil, nil, &s)

	if s.Process.Env[2] != "env2=2" {
		t.Fatal("couldn't update")
	}

	WithEnv([]string{"env2"})(nil, nil, nil, &s)

	if len(s.Process.Env) != 2 {
		t.Fatal("couldn't unset")
	}
}

func TestWithMounts(t *testing.T) {

	t.Parallel()

	s := Spec{
		Mounts: []specs.Mount{
			{
				Source:      "default-source",
				Destination: "default-dest",
			},
		},
	}

	WithMounts([]specs.Mount{
		{
			Source:      "new-source",
			Destination: "new-dest",
		},
	})(nil, nil, nil, &s)

	if len(s.Mounts) != 2 {
		t.Fatal("didn't append")
	}

	if s.Mounts[1].Source != "new-source" {
		t.Fatal("invalid mount")
	}

	if s.Mounts[1].Destination != "new-dest" {
		t.Fatal("invalid mount")
	}
}

func TestWithDefaultSpec(t *testing.T) {
	t.Parallel()
	var (
		s   Spec
		c   = containers.Container{ID: "TestWithDefaultSpec"}
		ctx = namespaces.WithNamespace(context.Background(), "test")
	)

	if err := ApplyOpts(ctx, nil, &c, &s, WithDefaultSpec()); err != nil {
		t.Fatal(err)
	}

	var expected Spec
	var err error
	if runtime.GOOS == "windows" {
		err = populateDefaultWindowsSpec(ctx, &expected, c.ID)
	} else {
		err = populateDefaultUnixSpec(ctx, &expected, c.ID)
	}
	if err != nil {
		t.Fatal(err)
	}

	if reflect.DeepEqual(s, Spec{}) {
		t.Fatalf("spec should not be empty")
	}

	if !reflect.DeepEqual(&s, &expected) {
		t.Fatalf("spec from option differs from default: \n%#v != \n%#v", &s, expected)
	}
}

func TestWithSpecFromFile(t *testing.T) {
	t.Parallel()
	var (
		s   Spec
		c   = containers.Container{ID: "TestWithDefaultSpec"}
		ctx = namespaces.WithNamespace(context.Background(), "test")
	)

	fp, err := ioutil.TempFile("", "testwithdefaultspec.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Remove(fp.Name()); err != nil {
			log.Printf("failed to remove tempfile %v: %v", fp.Name(), err)
		}
	}()
	defer fp.Close()

	expected, err := GenerateSpec(ctx, nil, &c)
	if err != nil {
		t.Fatal(err)
	}

	p, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fp.Write(p); err != nil {
		t.Fatal(err)
	}

	if err := ApplyOpts(ctx, nil, &c, &s, WithSpecFromFile(fp.Name())); err != nil {
		t.Fatal(err)
	}

	if reflect.DeepEqual(s, Spec{}) {
		t.Fatalf("spec should not be empty")
	}

	if !reflect.DeepEqual(&s, expected) {
		t.Fatalf("spec from option differs from default: \n%#v != \n%#v", &s, expected)
	}
}

func TestWithMemoryLimit(t *testing.T) {
	var (
		ctx = namespaces.WithNamespace(context.Background(), "testing")
		c   = containers.Container{ID: t.Name()}
		m   = uint64(768 * 1024 * 1024)
		o   = WithMemoryLimit(m)
	)
	// Test with all three supported scenarios
	platforms := []string{"", "linux/amd64", "windows/amd64"}
	for _, p := range platforms {
		var spec *Spec
		var err error
		if p == "" {
			t.Log("Testing GenerateSpec default platform")
			spec, err = GenerateSpec(ctx, nil, &c, o)

			// Convert the platform to the default based on GOOS like
			// GenerateSpec does.
			switch runtime.GOOS {
			case "linux":
				p = "linux/amd64"
			case "windows":
				p = "windows/amd64"
			}
		} else {
			t.Logf("Testing GenerateSpecWithPlatform with platform: '%s'", p)
			spec, err = GenerateSpecWithPlatform(ctx, nil, p, &c, o)
		}
		if err != nil {
			t.Fatalf("failed to generate spec with: %v", err)
		}
		switch p {
		case "linux/amd64":
			if *spec.Linux.Resources.Memory.Limit != int64(m) {
				t.Fatalf("spec.Linux.Resources.Memory.Limit expected: %v, got: %v", m, *spec.Linux.Resources.Memory.Limit)
			}
			// If we are linux/amd64 on Windows GOOS it is LCOW
			if runtime.GOOS == "windows" {
				// Verify that we also set the Windows section.
				if *spec.Windows.Resources.Memory.Limit != m {
					t.Fatalf("for LCOW spec.Windows.Resources.Memory.Limit is also expected: %v, got: %v", m, *spec.Windows.Resources.Memory.Limit)
				}
			} else {
				if spec.Windows != nil {
					t.Fatalf("spec.Windows section should not be set for linux/amd64 spec on non-windows platform")
				}
			}
		case "windows/amd64":
			if *spec.Windows.Resources.Memory.Limit != m {
				t.Fatalf("spec.Windows.Resources.Memory.Limit expected: %v, got: %v", m, *spec.Windows.Resources.Memory.Limit)
			}
			if spec.Linux != nil {
				t.Fatalf("spec.Linux section should not be set for windows/amd64 spec ever")
			}
		}
	}
}
