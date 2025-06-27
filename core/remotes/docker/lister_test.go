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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/containerd/containerd/v2/pkg/reference"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListingTags(t *testing.T) {
	testCases := []struct {
		name       string
		tags       TagList
		status     int
		hosts      func(s *httptest.Server) []RegistryHost
		assertErr  func(t *testing.T, err error)
		assertTags func(t *testing.T, tags []string)
	}{
		{
			name:   "success",
			status: http.StatusOK,
			tags: TagList{
				Name: "docker.io/library/alpine",
				Tags: []string{"latest", "v0.0.1"},
			},
			hosts: func(s *httptest.Server) []RegistryHost {
				u, err := url.Parse(s.URL)
				if err != nil {
					t.Fatal(err)
				}

				return []RegistryHost{
					{
						Client:       s.Client(),
						Host:         u.Host,
						Scheme:       u.Scheme,
						Path:         u.Path,
						Capabilities: HostCapabilityPull | HostCapabilityResolve,
					},
				}
			},
			assertTags: func(t *testing.T, tags []string) {
				assert.Equal(t, tags, []string{"latest", "v0.0.1"})
			},
			assertErr: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:   "no tags",
			status: http.StatusOK,
			tags: TagList{
				Name: "docker.io/library/alpine",
				Tags: []string{},
			},
			assertTags: func(t *testing.T, tags []string) {
				assert.Equal(t, tags, []string{})
			},
			assertErr: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
			hosts: func(s *httptest.Server) []RegistryHost {
				u, err := url.Parse(s.URL)
				if err != nil {
					t.Fatal(err)
				}

				return []RegistryHost{
					{
						Client:       s.Client(),
						Host:         u.Host,
						Scheme:       u.Scheme,
						Path:         u.Path,
						Capabilities: HostCapabilityPull | HostCapabilityResolve,
					},
				}
			},
		},
		{
			name:   "no capabilities",
			status: http.StatusOK,
			tags: TagList{
				Name: "docker.io/library/alpine",
				Tags: []string{"latest", "v0.0.1"},
			},
			assertTags: func(t *testing.T, tags []string) {
				assert.Empty(t, tags)
			},
			assertErr: func(t *testing.T, err error) {
				assert.EqualError(t, err, "no list hosts: not found")
			},
			hosts: func(s *httptest.Server) []RegistryHost {
				u, err := url.Parse(s.URL)
				if err != nil {
					t.Fatal(err)
				}

				return []RegistryHost{
					{
						Client: s.Client(),
						Host:   u.Host,
						Scheme: u.Scheme,
						Path:   u.Path,
					},
				}
			},
		},
		{
			name:   "not found",
			status: http.StatusNotFound,
			assertTags: func(t *testing.T, tags []string) {
				assert.Empty(t, tags)
			},
			assertErr: func(t *testing.T, err error) {
				assert.EqualError(t, err, "docker.io/library/alpine: not found")
			},
			hosts: func(s *httptest.Server) []RegistryHost {
				u, err := url.Parse(s.URL)
				if err != nil {
					t.Fatal(err)
				}

				return []RegistryHost{
					{
						Client:       s.Client(),
						Host:         u.Host,
						Scheme:       u.Scheme,
						Path:         u.Path,
						Capabilities: HostCapabilityPull | HostCapabilityResolve,
					},
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				rw.WriteHeader(tc.status)
				bytes, _ := json.Marshal(tc.tags)
				rw.Write(bytes)
			}))
			defer s.Close()

			parse, _ := reference.Parse("docker.io/library/alpine")

			f := dockerLister{&dockerBase{
				repository: "ns",
				refspec:    parse,
				hosts:      tc.hosts(s),
			}}

			tags, err := f.List(context.Background())
			tc.assertErr(t, err)
			tc.assertTags(t, tags)
		})
	}

}
