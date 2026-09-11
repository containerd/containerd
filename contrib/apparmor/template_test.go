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
			doc:     "unconfined newline",
			profile: "unconfined\n",
			want:    "unconfined",
		},
		{
			doc:     "unconfined enforce",
			profile: "unconfined (enforce)",
			want:    "unconfined",
		},
		{
			doc:     "unconfined enforce newline",
			profile: "unconfined (enforce)\n",
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
		{
			doc:     "spaces",
			profile: "with spaces (enforce)",
			want:    "with spaces",
		},
		{
			doc:     "parentheses in name",
			profile: "foo (bar) (enforce)",
			want:    "foo (bar)",
		},
		{
			doc:     "unknown mode",
			profile: "foo (anything)",
			want:    "foo",
		},
	}

	for _, tc := range tests {
		t.Run(tc.doc, func(t *testing.T) {
			assert.Equal(t, tc.want, cleanProfileName(tc.profile))
		})
	}
}

func TestQuote(t *testing.T) {
	tests := []struct {
		doc   string
		value string
		want  string
	}{
		{
			doc:  "empty",
			want: "",
		},
		{
			doc:   "simple",
			value: "default-profile",
			want:  `"default-profile"`,
		},
		{
			doc:   "spaces",
			value: "with spaces",
			want:  `"with spaces"`,
		},
		{
			doc:   "double quote",
			value: `foo"bar`,
			want:  `"foo\"bar"`,
		},
		{
			doc:   "backslash",
			value: `foo\bar`,
			want:  `"foo\\bar"`,
		},
		{
			doc:   "escape sequence",
			value: `foo\nbar`,
			want:  `"foo\\nbar"`,
		},
		{
			doc:   "AARE pattern",
			value: `foo*bar`,
			want:  `"foo*bar"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.doc, func(t *testing.T) {
			assert.Equal(t, tc.want, quote(tc.value))
		})
	}
}
