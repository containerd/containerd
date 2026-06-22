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
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestGenerateTokenOptions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		realm    string
		service  string
		username string
		secret   string
		scope    string
	}{
		{
			name:     "MultipleScopes",
			realm:    "https://test-realm.com",
			service:  "registry-service",
			username: "username",
			secret:   "secret",
			scope:    "repository:foo/bar:pull repository:foo/bar:pull,push",
		},
		{
			name:     "SingleScope",
			realm:    "https://test-realm.com",
			service:  "registry-service",
			username: "username",
			secret:   "secret",
			scope:    "repository:foo/bar:pull",
		},
		{
			name:     "NoScope",
			realm:    "https://test-realm.com",
			service:  "registry-service",
			username: "username",
			secret:   "secret",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Challenge{
				Scheme: BearerAuth,
				Parameters: map[string]string{
					"realm":   tc.realm,
					"service": tc.service,
					"scope":   tc.scope,
				},
			}
			options, err := GenerateTokenOptions(context.Background(), "host", tc.username, tc.secret, c)
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}

			expected := TokenOptions{
				Realm:    tc.realm,
				Service:  tc.service,
				Scopes:   strings.Split(tc.scope, " "),
				Username: tc.username,
				Secret:   tc.secret,
			}
			if !reflect.DeepEqual(options, expected) {
				t.Fatalf("expected %v, but got %v", expected, options)
			}
		})
	}

	t.Run("MissingRealm", func(t *testing.T) {
		c := Challenge{
			Scheme: BearerAuth,
			Parameters: map[string]string{
				"service": "service",
				"scope":   "repository:foo/bar:pull,push",
			},
		}
		_, err := GenerateTokenOptions(context.Background(), "host", "username", "secret", c)
		if err == nil {
			t.Fatal("expected an err and got nil")
		}
	})

	t.Run("RealmParseError", func(t *testing.T) {
		c := Challenge{
			Scheme: BearerAuth,
			Parameters: map[string]string{
				"realm":   "127.0.0.1:8080",
				"service": "service",
				"scope":   "repository:foo/bar:pull,push",
			},
		}
		_, err := GenerateTokenOptions(context.Background(), "host", "username", "secret", c)
		if err == nil {
			t.Fatal("expected an err and got nil")
		}
	})
}
