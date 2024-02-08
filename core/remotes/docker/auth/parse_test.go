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

package auth

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseAuthHeaderBearer(t *testing.T) {
	headerTemplate := `Bearer realm="%s",service="%s",scope="%s"`

	for _, tc := range []struct {
		name    string
		realm   string
		service string
		scope   string
	}{
		{
			name:    "SingleScope",
			realm:   "https://auth.docker.io/token",
			service: "registry.docker.io",
			scope:   "repository:foo/bar:pull,push",
		},
		{
			name:    "MultipleScopes",
			realm:   "https://auth.docker.io/token",
			service: "registry.docker.io",
			scope:   "repository:foo/bar:pull,push repository:foo/baz:pull repository:foo/foo:push",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected := []Challenge{
				{
					Scheme: BearerAuth,
					Parameters: map[string]string{
						"realm":   tc.realm,
						"service": tc.service,
						"scope":   tc.scope,
					},
				},
			}

			hdr := http.Header{
				http.CanonicalHeaderKey("WWW-Authenticate"): []string{fmt.Sprintf(
					headerTemplate, tc.realm, tc.service, tc.scope,
				)},
			}
			actual := ParseAuthHeader(hdr)
			if !reflect.DeepEqual(expected, actual) {
				t.Fatalf("expected %v, but got %v", expected, actual)
			}
		})
	}
}

func TestParseAuthHeader(t *testing.T) {
	v := `Bearer realm="https://auth.example.io/token",empty="",service="registry.example.io",scope="repository:library/hello-world:pull,push"`
	h := http.Header{http.CanonicalHeaderKey("WWW-Authenticate"): []string{v}}
	challenge := ParseAuthHeader(h)

	actual, ok := challenge[0].Parameters["empty"]
	assert.True(t, ok)
	assert.Equal(t, "", actual)

	actual, ok = challenge[0].Parameters["service"]
	assert.True(t, ok)
	assert.Equal(t, "registry.example.io", actual)
}

func FuzzParseAuthHeader(f *testing.F) {
	f.Add(`Bearer realm="https://example.com/token",service="example.com",scope="repository:foo/bar:pull,push"`)
	f.Fuzz(func(t *testing.T, v string) {
		h := http.Header{http.CanonicalHeaderKey("WWW-Authenticate"): []string{v}}
		_ = ParseAuthHeader(h)
	})
}
