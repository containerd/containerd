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
	"net/http"
	"net/url"
	"testing"

	"github.com/containerd/containerd/remotes/docker/auth"
	"github.com/containerd/containerd/remotes/docker/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthHandlerAuthorizationHeader(t *testing.T) {
	handler := newAuthHandler(nil, http.Header{}, auth.BearerAuth, auth.TokenOptions{}, "foo")

	ctx := context.Background()
	header, refreshToken, err := handler.authorize(ctx)

	assert.NoError(t, err)
	assert.Equal(t, "", refreshToken)
	assert.Equal(t, "foo", header)
}

func TestDockerAuthorizerAddResponses(t *testing.T) {
	const host = "https://test-default.registry"
	credentialHelper := credentials.NewMemoryStore()

	require.NoError(t, credentialHelper.Set(&credentials.Credentials{
		ServerURL: host,
		Header:    "Bearer foo",
	}))

	authOpts := []AuthorizerOpt{
		WithAuthCredentials(func(server string) (*credentials.Credentials, error) {
			creds, err := credentialHelper.Get(server)
			if err != nil {
				return nil, err
			}
			return creds, nil
		}),
	}

	authorizer := NewDockerAuthorizer(authOpts...)

	ctx := context.Background()
	err := authorizer.AddResponses(ctx, []*http.Response{
		{
			Request: &http.Request{
				URL: &url.URL{
					Host: host,
				},
			},
			Header: map[string][]string{
				"Www-Authenticate": {
					"Bearer realm=\"auth.test-default.registry\"",
				},
			},
		},
	})
	assert.NoError(t, err)

	dockerAuth, ok := authorizer.(*dockerAuthorizer)
	assert.True(t, ok)
	assert.Equal(t, 1, len(dockerAuth.handlers))

	handler := dockerAuth.getAuthHandler(host)
	assert.NotNil(t, handler)
	assert.Equal(t, handler.authorization, "Bearer foo")

	header, refreshToken, err := handler.authorize(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "", refreshToken)
	assert.Equal(t, "Bearer foo", header)
}
