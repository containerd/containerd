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

package archive

import (
	"testing"

	"github.com/opencontainers/go-digest"
)

const testDigest = digest.Digest("sha256:a443fa8f04d2c2fd288b6f0f73e6f42d1b4a0b0c1a2e3f405162738495a6b7c8")

func TestOCIReferenceName(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: "docker.io/library/nginx:latest", want: "latest"},
		{input: "myrepo/nginx:tag", want: "tag"},
		{input: "localhost:5000/myimage", want: "localhost:5000/myimage"},
		{input: "nginx:latest", want: "latest"},
		{input: "myimage", want: "myimage"},
		{input: "localhost:5000/myimage", want: "localhost:5000/myimage"},
		{input: "docker.io/library/nginx@sha256:" + testDigest.Encoded(), want: "docker.io/library/nginx@sha256:" + testDigest.Encoded()},
		{input: "nginx@sha256:" + testDigest.Encoded(), want: "nginx@sha256:" + testDigest.Encoded()},
		{input: "docker.io/library/nginx:1.23@sha256:" + testDigest.Encoded(), want: "1.23@sha256:" + testDigest.Encoded()},
		{input: "not a valid ref!!", want: "not a valid ref!!"},
	} {
		if got := ociReferenceName(tc.input); got != tc.want {
			t.Errorf("ociReferenceName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestRefTranslator(t *testing.T) {
	for _, tc := range []struct {
		name  string
		image string
		input string
		fwant string
		awant string
	}{
		{name: "tag only gets prefix", image: "foo/bar", input: "mytag", fwant: "foo/bar:mytag", awant: "foo/bar:mytag"},
		{name: "full reference with prefix kept", image: "foo/bar", input: "foo/bar:tag", fwant: "foo/bar:tag", awant: "foo/bar:tag"},
		{name: "digest reference with prefix kept", image: "foo/bar", input: "foo/bar@sha256:" + testDigest.Encoded(), fwant: "foo/bar@sha256:" + testDigest.Encoded(), awant: "foo/bar@sha256:" + testDigest.Encoded()},
		{name: "unprefixed full reference filtered", image: "foo/bar", input: "other/image:tag", fwant: "", awant: "other/image:tag"},
		{name: "partial namespace not matched", image: "foo/bar", input: "foo/bar2:tag", fwant: "", awant: "foo/bar2:tag"},
		{name: "empty image returns empty", image: "", input: "mytag", fwant: "", awant: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FilterRefPrefix(tc.image)(tc.input); got != tc.fwant {
				t.Errorf("FilterRefPrefix(%q)(%q) = %q, want %q", tc.image, tc.input, got, tc.fwant)
			}
			if got := AddRefPrefix(tc.image)(tc.input); got != tc.awant {
				t.Errorf("AddRefPrefix(%q)(%q) = %q, want %q", tc.image, tc.input, got, tc.awant)
			}
		})
	}
}

func TestDigestTranslator(t *testing.T) {
	if got := DigestTranslator("foo/bar")(testDigest); got != "foo/bar@sha256:"+testDigest.Encoded() {
		t.Errorf("DigestTranslator = %q", got)
	}
}

func TestNormalizeReference(t *testing.T) {
	for _, tc := range []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: "nginx:latest", want: "docker.io/library/nginx:latest"},
		{input: "myrepo/nginx:tag", want: "docker.io/myrepo/nginx:tag"},
		{input: "localhost:5000/myimage", want: "localhost:5000/myimage:latest"},
		{input: "nginx@sha256:" + testDigest.Encoded(), want: "docker.io/library/nginx@sha256:" + testDigest.Encoded()},
		{input: "not a valid ref!!", wantErr: true},
	} {
		got, err := normalizeReference(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("normalizeReference(%q) expected error, got %q", tc.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeReference(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("normalizeReference(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestFamiliarizeReference(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: "docker.io/library/nginx:latest", want: "nginx:latest"},
		{input: "myrepo/nginx", want: "myrepo/nginx:latest"},
		{input: "localhost:5000/myimage:tag", want: "localhost:5000/myimage:tag"},
	} {
		got, err := familiarizeReference(tc.input)
		if err != nil {
			t.Errorf("familiarizeReference(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("familiarizeReference(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
