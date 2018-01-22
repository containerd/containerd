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
