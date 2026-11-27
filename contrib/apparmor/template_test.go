//go:build linux

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

package apparmor

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultProfileAllowsUnconfinedStackedPeers(t *testing.T) {
	var buf bytes.Buffer
	err := generate(&data{Name: "cri-containerd.apparmor.d"}, &buf)
	assert.NoError(t, err)
	prof := buf.String()
	assert.Contains(t, prof, "signal (send,receive) peer=cri-containerd.apparmor.d//&unconfined,")
	assert.Contains(t, prof, "ptrace (trace,tracedby,read,readby) peer=cri-containerd.apparmor.d//&unconfined,")
	assert.Contains(t, prof, "signal (send,receive) peer=cri-containerd.apparmor.d,")
	assert.Contains(t, prof, "ptrace (trace,tracedby,read,readby) peer=cri-containerd.apparmor.d,")
	assert.NotContains(t, prof, "//&*")
}

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

func TestQuoteProfileName(t *testing.T) {
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
			doc:   "AARE wildcard",
			value: `foo*bar?baz`,
			want:  `"foo\*bar\?baz"`,
		},
		{
			doc:   "AARE character class",
			value: `foo[bar]`,
			want:  `"foo\[bar\]"`,
		},
		{
			doc:   "AARE alternation",
			value: `foo{bar,baz}`,
			want:  `"foo\{bar\,baz\}"`,
		},
		{
			doc:   "AARE anchor",
			value: `foo^bar`,
			want:  `"foo\^bar"`,
		},
		{
			doc:   "invalid UTF-8 with special character",
			value: "foo\xff*bar",
			want:  "\"foo\xff\\*bar\"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.doc, func(t *testing.T) {
			assert.Equal(t, tc.want, quoteProfileName(tc.value))
		})
	}
}
