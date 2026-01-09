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

package docker

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestWarningHandler(t *testing.T) {
	name := "testname"
	tag := "latest"

	m := newManifest(
		newContent(ocispec.MediaTypeImageConfig, []byte("{}")),
		newContent(ocispec.MediaTypeImageLayerGzip, []byte("layer content")),
	)
	mc := newContent(ocispec.MediaTypeImageManifest, m.OCIManifest())

	mux := http.NewServeMux()
	addWarning := func(h http.Handler, msg string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Warning", fmt.Sprintf(`299 - "%s"`, msg))
			h.ServeHTTP(w, r)
		})
	}

	for _, c := range []testContent{m.config, m.references[0]} {
		mux.Handle(fmt.Sprintf("/v2/%s/blobs/%s", name, c.Digest()), addWarning(c, "Blob access warning"))
	}
	mux.Handle(fmt.Sprintf("/v2/%s/manifests/%s", name, tag), addWarning(mc, "Manifest access warning"))
	mux.Handle(fmt.Sprintf("/v2/%s/manifests/%s", name, mc.Digest()), addWarning(mc, "Manifest access warning"))

	s := httptest.NewServer(mux)
	defer s.Close()

	image := fmt.Sprintf("%s/%s:%s", strings.TrimPrefix(s.URL, "http://"), name, tag)

	type expectedWarning struct {
		warning string
		src     WarningSource
	}

	tests := []struct {
		name             string
		resolve          bool
		descriptors      []ocispec.Descriptor
		expectedWarnings []expectedWarning
	}{
		{
			name:    "Resolve",
			resolve: true,
			expectedWarnings: []expectedWarning{
				{warning: "Manifest access warning", src: WarningSource{URL: fmt.Sprintf("%s/v2/%s/manifests/%s", s.URL, name, tag)}},
			},
		},
		{
			name:        "Fetch manifest",
			descriptors: []ocispec.Descriptor{mc.Descriptor()},
			expectedWarnings: []expectedWarning{
				{warning: "Manifest access warning", src: WarningSource{URL: fmt.Sprintf("%s/v2/%s/manifests/%s", s.URL, name, mc.Digest())}},
			},
		},
		{
			name:        "Fetch blob",
			descriptors: []ocispec.Descriptor{m.config.Descriptor(), m.references[0].Descriptor()},
			expectedWarnings: []expectedWarning{
				{warning: "Blob access warning", src: WarningSource{URL: fmt.Sprintf("%s/v2/%s/blobs/%s", s.URL, name, m.config.Digest())}},
				{warning: "Blob access warning", src: WarningSource{URL: fmt.Sprintf("%s/v2/%s/blobs/%s", s.URL, name, m.references[0].Digest())}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			type capturedWarning struct {
				src     WarningSource
				warning string
			}
			var warnings []capturedWarning
			resolver := NewResolver(ResolverOptions{
				WarningHandler: warningHandlerFunc(func(src WarningSource, warning string) {
					warnings = append(warnings, capturedWarning{src: src, warning: warning})
				}),
			})

			ctx := t.Context()

			if tc.resolve {
				if _, _, err := resolver.Resolve(ctx, image); err != nil {
					t.Fatal(err)
				}
			}

			if len(tc.descriptors) > 0 {
				fetcher, err := resolver.Fetcher(ctx, image)
				if err != nil {
					t.Fatal(err)
				}
				for _, desc := range tc.descriptors {
					rc, err := fetcher.Fetch(ctx, desc)
					if err != nil {
						t.Fatal(err)
					}
					io.Copy(io.Discard, rc)
					rc.Close()
				}
			}

			if len(warnings) != len(tc.expectedWarnings) {
				t.Fatalf("Expected %d warnings, got %d: %v", len(tc.expectedWarnings), len(warnings), warnings)
			}

			for i, w := range warnings {
				expected := tc.expectedWarnings[i]
				if w.warning != expected.warning {
					t.Fatalf("Expected warning %q, got: %s", expected.warning, w.warning)
				}
				if !strings.Contains(w.src.URL, expected.src.URL) {
					t.Fatalf("Expected warning source URL to contain %q, got: %s", expected.src.URL, w.src.URL)
				}
			}
		})
	}
}

func TestParseWarningText(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "simple warning",
			header: `299 - "message"`,
			want:   "message",
		},
		{
			name:   "prefixed header text invalid",
			header: `foobar 299 - "prefixed message"`,
			want:   "",
		},
		{
			name:   "escaped quotes preserved",
			header: `299 - "quoted \"text\" inside"`,
			want:   `quoted "text" inside`,
		},
		{
			name:   "escaped backslash",
			header: `299 - "path\\to\\file"`,
			want:   `path\to\file`,
		},
		{
			name:   "percent-encoded space",
			header: `299 - "hello%20world"`,
			want:   "hello world",
		},
		{
			name:   "percent-encoded special chars",
			header: `299 - "100%25 complete"`,
			want:   "100% complete",
		},
		{
			name:   "percent-encoded quote",
			header: `299 - "say %22hello%22"`,
			want:   `say "hello"`,
		},
		{
			name:   "invalid percent-encoding treated as literal",
			header: `299 - "100%ZZ invalid"`,
			want:   "100%ZZ invalid",
		},
		{
			name:   "incomplete percent-encoding at end",
			header: `299 - "trailing%2"`,
			want:   "trailing%2",
		},
		{
			name:   "trailing characters invalid",
			header: `299 - "warn text" extra data`,
			want:   "",
		},
		{
			name:   "non oci warning code",
			header: `199 - "ignored"`,
			want:   "",
		},
		{
			name:   "missing closing quote",
			header: `299 - "incomplete`,
			want:   "",
		},
		{
			name:   "missing opening quote",
			header: `299 - warn text"`,
			want:   "",
		},
		{
			name:   "empty input",
			header: "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseWarningText(tt.header)
			if got != tt.want {
				t.Fatalf("parseWarningText(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}
