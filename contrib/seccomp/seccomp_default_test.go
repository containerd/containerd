package seccomp

import (
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
)

func TestIOUringIsNotAllowed(t *testing.T) {

	disallowed := map[string]bool{
		"io_uring_enter":    true,
		"io_uring_register": true,
		"io_uring_setup":    true,
	}

	got := DefaultProfile(&specs.Spec{
		Process: &specs.Process{
			Capabilities: &specs.LinuxCapabilities{
				Bounding: []string{},
			},
		},
	})

	for _, config := range got.Syscalls {
		if config.Action != specs.ActAllow {
			continue
		}

		for _, name := range config.Names {
			if disallowed[name] {
				t.Errorf("found disallowed io_uring related syscalls")
			}
		}
	}
}
