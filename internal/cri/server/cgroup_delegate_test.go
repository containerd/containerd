package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCgroupDelegateAnnotations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		cgroupWritable  bool
		unifiedCgroups  bool
		privileged      bool
		wantAnnotations map[string]string
	}{
		{
			name:            "writable cgroup v2 unprivileged",
			cgroupWritable:  true,
			unifiedCgroups:  true,
			privileged:      false,
			wantAnnotations: map[string]string{"org.systemd.property.Delegate": "true"},
		},
		{
			name:           "writable cgroup v2 privileged",
			cgroupWritable: true,
			unifiedCgroups: true,
			privileged:     true,
		},
		{
			name:           "writable cgroup v1",
			cgroupWritable: true,
			unifiedCgroups: false,
			privileged:     false,
		},
		{
			name:           "read-only cgroup v2",
			cgroupWritable: false,
			unifiedCgroups: true,
			privileged:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.wantAnnotations, cgroupDelegateAnnotations(tc.cgroupWritable, tc.unifiedCgroups, tc.privileged))
		})
	}
}
