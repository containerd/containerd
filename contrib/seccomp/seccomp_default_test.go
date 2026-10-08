//go:build linux

package seccomp

import (
	"reflect"
	"testing"

	"golang.org/x/sys/unix"

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

func TestSocketSyscallsForDomains(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		domains []uint64
		want    []specs.LinuxSyscall
	}{
		{
			name:    "singleton",
			domains: []uint64{39},
			want:    []specs.LinuxSyscall{socketSyscall(39, specs.OpEqualTo)},
		},
		{
			name:    "later consecutive run",
			domains: []uint64{1, 2, 4, 5},
			want: []specs.LinuxSyscall{
				socketSyscall(3, specs.OpLessThan),
				socketSyscall(4, specs.OpEqualTo),
				socketSyscall(5, specs.OpEqualTo),
			},
		},
		{
			name:    "multiple gaps",
			domains: []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 13, 14, 16, 20},
			want: []specs.LinuxSyscall{
				socketSyscall(11, specs.OpLessThan),
				socketSyscall(12, specs.OpEqualTo),
				socketSyscall(13, specs.OpEqualTo),
				socketSyscall(14, specs.OpEqualTo),
				socketSyscall(16, specs.OpEqualTo),
				socketSyscall(20, specs.OpEqualTo),
			},
		},
		{
			name:    "two-domain initial range",
			domains: []uint64{1, 2},
			want:    []specs.LinuxSyscall{socketSyscall(3, specs.OpLessThan)},
		},
		{
			name:    "gap after initial domain",
			domains: []uint64{1, 3, 4},
			want: []specs.LinuxSyscall{
				socketSyscall(1, specs.OpEqualTo),
				socketSyscall(3, specs.OpEqualTo),
				socketSyscall(4, specs.OpEqualTo),
			},
		},
		{
			name:    "range not at initial domain",
			domains: []uint64{10, 11, 12},
			want: []specs.LinuxSyscall{
				socketSyscall(10, specs.OpEqualTo),
				socketSyscall(11, specs.OpEqualTo),
				socketSyscall(12, specs.OpEqualTo),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := socketSyscallsForDomains(tt.domains); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("socketSyscallsForDomains(%v) = %#v, want %#v", tt.domains, got, tt.want)
			}
		})
	}
}

func TestSocketSyscalls(t *testing.T) {
	t.Parallel()

	want := []specs.LinuxSyscall{
		socketSyscall(unix.AF_ALG, specs.OpLessThan),
		socketSyscall(unix.AF_NFC, specs.OpEqualTo),
		socketSyscall(unix.AF_KCM, specs.OpEqualTo),
		socketSyscall(unix.AF_QIPCRTR, specs.OpEqualTo),
		socketSyscall(unix.AF_SMC, specs.OpEqualTo),
		socketSyscall(unix.AF_XDP, specs.OpEqualTo),
		socketSyscall(unix.AF_MCTP, specs.OpEqualTo),
	}
	if got := socketSyscalls(); !reflect.DeepEqual(got, want) {
		t.Errorf("socketSyscalls() = %#v, want %#v", got, want)
	}
}
