package containerd

import (
	"reflect"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestWithMount(t *testing.T) {
	mount := specs.Mount{
		Destination: "/test",
		Type:        "sysfs",
		Source:      "sysfs",
	}
	s, err := GenerateSpec(WithMount(mount))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Mounts) == 0 || !reflect.DeepEqual(mount, s.Mounts[len(s.Mounts)-1]) {
		t.Errorf("mount is not added")
	}
}
