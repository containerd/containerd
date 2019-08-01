/*
Copyright 2017 The Kubernetes Authors.

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

package server

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"

	criconfig "github.com/containerd/cri/pkg/config"
)

func TestParseAuth(t *testing.T) {
	testUser := "username"
	testPasswd := "password"
	testAuthLen := base64.StdEncoding.EncodedLen(len(testUser + ":" + testPasswd))
	testAuth := make([]byte, testAuthLen)
	base64.StdEncoding.Encode(testAuth, []byte(testUser+":"+testPasswd))
	invalidAuth := make([]byte, testAuthLen)
	base64.StdEncoding.Encode(invalidAuth, []byte(testUser+"@"+testPasswd))
	for desc, test := range map[string]struct {
		auth           *runtime.AuthConfig
		expectedUser   string
		expectedSecret string
		expectErr      bool
	}{
		"should not return error if auth config is nil": {},
		"should return error if no supported auth is provided": {
			auth:      &runtime.AuthConfig{},
			expectErr: true,
		},
		"should support identity token": {
			auth:           &runtime.AuthConfig{IdentityToken: "abcd"},
			expectedSecret: "abcd",
		},
		"should support username and password": {
			auth: &runtime.AuthConfig{
				Username: testUser,
				Password: testPasswd,
			},
			expectedUser:   testUser,
			expectedSecret: testPasswd,
		},
		"should support auth": {
			auth:           &runtime.AuthConfig{Auth: string(testAuth)},
			expectedUser:   testUser,
			expectedSecret: testPasswd,
		},
		"should return error for invalid auth": {
			auth:      &runtime.AuthConfig{Auth: string(invalidAuth)},
			expectErr: true,
		},
	} {
		t.Logf("TestCase %q", desc)
		u, s, err := ParseAuth(test.auth)
		assert.Equal(t, test.expectErr, err != nil)
		assert.Equal(t, test.expectedUser, u)
		assert.Equal(t, test.expectedSecret, s)
	}
}

func TestCredentials(t *testing.T) {
	c := newTestCRIService()
	c.config.Registry.Auths = map[string]criconfig.AuthConfig{
		"https://test1.io": {
			Username: "username1",
			Password: "password1",
		},
		"http://test2.io": {
			Username: "username2",
			Password: "password2",
		},
		"//test3.io": {
			Username: "username3",
			Password: "password3",
		},
	}
	for desc, test := range map[string]struct {
		auth             *runtime.AuthConfig
		host             string
		expectedUsername string
		expectedPassword string
	}{
		"auth config from CRI should take precedence": {
			auth: &runtime.AuthConfig{
				Username: "username",
				Password: "password",
			},
			host:             "test1.io",
			expectedUsername: "username",
			expectedPassword: "password",
		},
		"should support https host": {
			host:             "test1.io",
			expectedUsername: "username1",
			expectedPassword: "password1",
		},
		"should support http host": {
			host:             "test2.io",
			expectedUsername: "username2",
			expectedPassword: "password2",
		},
		"should support hostname only": {
			host:             "test3.io",
			expectedUsername: "username3",
			expectedPassword: "password3",
		},
	} {
		t.Logf("TestCase %q", desc)
		username, password, err := c.credentials(test.auth)(test.host)
		assert.NoError(t, err)
		assert.Equal(t, test.expectedUsername, username)
		assert.Equal(t, test.expectedPassword, password)
	}
}
