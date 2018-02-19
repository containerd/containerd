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
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestWithEnv(t *testing.T) {
	t.Parallel()

	s := specs.Spec{}
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

	s := specs.Spec{
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
