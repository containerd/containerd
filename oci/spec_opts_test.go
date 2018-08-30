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
		t.Fatal("could't update")
	}

	WithEnv([]string{"env2"})(nil, nil, nil, &s)

	if len(s.Process.Env) != 2 {
		t.Fatal("coudn't unset")
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
		t.Fatal("invaid mount")
	}

	if s.Mounts[1].Destination != "new-dest" {
		t.Fatal("invaid mount")
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
