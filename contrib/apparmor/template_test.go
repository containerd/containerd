//go:build linux

package apparmor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanProfileName(t *testing.T) {
	tests := []struct {
		doc     string
		profile string
		want    string
	}{
		{
			doc:  "empty",
			want: "unconfined",
		},
		{
			doc:     "unconfined",
			profile: "unconfined",
			want:    "unconfined",
		},
		{
			doc:     "unconfined enforce",
			profile: "unconfined (enforce)",
			want:    "unconfined",
		},
		{
			doc:     "simple",
			profile: "docker-default",
			want:    "docker-default",
		},
		{
			doc:     "simple enforce",
			profile: "docker-default (enforce)",
			want:    "docker-default",
		},
	}

	for _, tc := range tests {
		t.Run(tc.doc, func(t *testing.T) {
			assert.Equal(t, tc.want, cleanProfileName(tc.profile))
		})
	}
}
