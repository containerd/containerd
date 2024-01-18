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

package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/containerd/imgcrypt"

	"github.com/containerd/containerd/pkg/cri/annotations"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/labels"

	encconfig "github.com/containers/ocicrypt/config"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
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
		host           string
		expectedUser   string
		expectedSecret string
		expectErr      bool
	}{
		"should not return error if auth config is nil": {},
		"should not return error if empty auth is provided for access to anonymous registry": {
			auth:      &runtime.AuthConfig{},
			expectErr: false,
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
		"should return empty auth if server address doesn't match": {
			auth: &runtime.AuthConfig{
				Username:      testUser,
				Password:      testPasswd,
				ServerAddress: "https://registry-1.io",
			},
			host:           "registry-2.io",
			expectedUser:   "",
			expectedSecret: "",
		},
		"should return auth if server address matches": {
			auth: &runtime.AuthConfig{
				Username:      testUser,
				Password:      testPasswd,
				ServerAddress: "https://registry-1.io",
			},
			host:           "registry-1.io",
			expectedUser:   testUser,
			expectedSecret: testPasswd,
		},
		"should return auth if server address is not specified": {
			auth: &runtime.AuthConfig{
				Username: testUser,
				Password: testPasswd,
			},
			host:           "registry-1.io",
			expectedUser:   testUser,
			expectedSecret: testPasswd,
		},
	} {
		t.Run(desc, func(t *testing.T) {
			u, s, err := ParseAuth(test.auth, test.host)
			assert.Equal(t, test.expectErr, err != nil)
			assert.Equal(t, test.expectedUser, u)
			assert.Equal(t, test.expectedSecret, s)
		})
	}
}

func TestRegistryEndpoints(t *testing.T) {
	for desc, test := range map[string]struct {
		mirrors  map[string]criconfig.Mirror
		host     string
		expected []string
	}{
		"no mirror configured": {
			mirrors: map[string]criconfig.Mirror{
				"registry-1.io": {
					Endpoints: []string{
						"https://registry-1.io",
						"https://registry-2.io",
					},
				},
			},
			host: "registry-3.io",
			expected: []string{
				"https://registry-3.io",
			},
		},
		"mirror configured": {
			mirrors: map[string]criconfig.Mirror{
				"registry-3.io": {
					Endpoints: []string{
						"https://registry-1.io",
						"https://registry-2.io",
					},
				},
			},
			host: "registry-3.io",
			expected: []string{
				"https://registry-1.io",
				"https://registry-2.io",
				"https://registry-3.io",
			},
		},
		"wildcard mirror configured": {
			mirrors: map[string]criconfig.Mirror{
				"*": {
					Endpoints: []string{
						"https://registry-1.io",
						"https://registry-2.io",
					},
				},
			},
			host: "registry-3.io",
			expected: []string{
				"https://registry-1.io",
				"https://registry-2.io",
				"https://registry-3.io",
			},
		},
		"host should take precedence if both host and wildcard mirrors are configured": {
			mirrors: map[string]criconfig.Mirror{
				"*": {
					Endpoints: []string{
						"https://registry-1.io",
					},
				},
				"registry-3.io": {
					Endpoints: []string{
						"https://registry-2.io",
					},
				},
			},
			host: "registry-3.io",
			expected: []string{
				"https://registry-2.io",
				"https://registry-3.io",
			},
		},
		"default endpoint in list with http": {
			mirrors: map[string]criconfig.Mirror{
				"registry-3.io": {
					Endpoints: []string{
						"https://registry-1.io",
						"https://registry-2.io",
						"http://registry-3.io",
					},
				},
			},
			host: "registry-3.io",
			expected: []string{
				"https://registry-1.io",
				"https://registry-2.io",
				"http://registry-3.io",
			},
		},
		"default endpoint in list with https": {
			mirrors: map[string]criconfig.Mirror{
				"registry-3.io": {
					Endpoints: []string{
						"https://registry-1.io",
						"https://registry-2.io",
						"https://registry-3.io",
					},
				},
			},
			host: "registry-3.io",
			expected: []string{
				"https://registry-1.io",
				"https://registry-2.io",
				"https://registry-3.io",
			},
		},
		"default endpoint in list with path": {
			mirrors: map[string]criconfig.Mirror{
				"registry-3.io": {
					Endpoints: []string{
						"https://registry-1.io",
						"https://registry-2.io",
						"https://registry-3.io/path",
					},
				},
			},
			host: "registry-3.io",
			expected: []string{
				"https://registry-1.io",
				"https://registry-2.io",
				"https://registry-3.io/path",
			},
		},
		"miss scheme endpoint in list with path": {
			mirrors: map[string]criconfig.Mirror{
				"registry-3.io": {
					Endpoints: []string{
						"https://registry-3.io",
						"registry-1.io",
						"127.0.0.1:1234",
					},
				},
			},
			host: "registry-3.io",
			expected: []string{
				"https://registry-3.io",
				"https://registry-1.io",
				"http://127.0.0.1:1234",
			},
		},
	} {
		t.Run(desc, func(t *testing.T) {
			c := newTestCRIService()
			c.config.Registry.Mirrors = test.mirrors
			got, err := c.registryEndpoints(test.host)
			assert.NoError(t, err)
			assert.Equal(t, test.expected, got)
		})
	}
}

func TestDefaultScheme(t *testing.T) {
	for desc, test := range map[string]struct {
		host     string
		expected string
	}{
		"should use http by default for localhost": {
			host:     "localhost",
			expected: "http",
		},
		"should use http by default for localhost with port": {
			host:     "localhost:8080",
			expected: "http",
		},
		"should use http by default for 127.0.0.1": {
			host:     "127.0.0.1",
			expected: "http",
		},
		"should use http by default for 127.0.0.1 with port": {
			host:     "127.0.0.1:8080",
			expected: "http",
		},
		"should use http by default for ::1": {
			host:     "::1",
			expected: "http",
		},
		"should use http by default for ::1 with port": {
			host:     "[::1]:8080",
			expected: "http",
		},
		"should use https by default for remote host": {
			host:     "remote",
			expected: "https",
		},
		"should use https by default for remote host with port": {
			host:     "remote:8080",
			expected: "https",
		},
		"should use https by default for remote ip": {
			host:     "8.8.8.8",
			expected: "https",
		},
		"should use https by default for remote ip with port": {
			host:     "8.8.8.8:8080",
			expected: "https",
		},
	} {
		t.Run(desc, func(t *testing.T) {
			got := defaultScheme(test.host)
			assert.Equal(t, test.expected, got)
		})
	}
}

func TestSnapshotterFromPodSandboxConfig(t *testing.T) {
	defaultSnashotter := "native"
	runtimeSnapshotter := "devmapper"
	tests := []struct {
		desc              string
		podSandboxConfig  *runtime.PodSandboxConfig
		expectSnapshotter string
		expectErr         error
	}{
		{
			desc:              "should return default snapshotter for nil podSandboxConfig",
			expectSnapshotter: defaultSnashotter,
		},
		{
			desc:              "should return default snapshotter for nil podSandboxConfig.Annotations",
			podSandboxConfig:  &runtime.PodSandboxConfig{},
			expectSnapshotter: defaultSnashotter,
		},
		{
			desc: "should return default snapshotter for empty podSandboxConfig.Annotations",
			podSandboxConfig: &runtime.PodSandboxConfig{
				Annotations: make(map[string]string),
			},
			expectSnapshotter: defaultSnashotter,
		},
		{
			desc: "should return error for runtime not found",
			podSandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.RuntimeHandler: "runtime-not-exists",
				},
			},
			expectErr:         fmt.Errorf(`experimental: failed to get sandbox runtime for runtime-not-exists, err: no runtime for "runtime-not-exists" is configured`),
			expectSnapshotter: "",
		},
		{
			desc: "should return snapshotter provided in podSandboxConfig.Annotations",
			podSandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.RuntimeHandler: "exiting-runtime",
				},
			},
			expectSnapshotter: runtimeSnapshotter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			cri := newTestCRIService()
			cri.config.ContainerdConfig.Snapshotter = defaultSnashotter
			cri.config.ContainerdConfig.Runtimes = make(map[string]criconfig.Runtime)
			cri.config.ContainerdConfig.Runtimes["exiting-runtime"] = criconfig.Runtime{
				Snapshotter: runtimeSnapshotter,
			}
			snapshotter, err := cri.snapshotterFromPodSandboxConfig(context.Background(), "test-image", tt.podSandboxConfig)
			assert.Equal(t, tt.expectSnapshotter, snapshotter)
			assert.Equal(t, tt.expectErr, err)
		})
	}
}
func TestImageGetLabels(t *testing.T) {

	criService := newTestCRIService()

	tests := []struct {
		name               string
		expectedLabel      map[string]string
		configSandboxImage string
		pullImageName      string
	}{
		{
			name:               "pinned image labels should get added on sandbox image",
			expectedLabel:      map[string]string{labels.ImageLabelKey: labels.ImageLabelValue, labels.PinnedImageLabelKey: labels.PinnedImageLabelValue},
			configSandboxImage: "k8s.gcr.io/pause:3.9",
			pullImageName:      "k8s.gcr.io/pause:3.9",
		},
		{
			name:               "pinned image labels should get added on sandbox image without tag",
			expectedLabel:      map[string]string{labels.ImageLabelKey: labels.ImageLabelValue, labels.PinnedImageLabelKey: labels.PinnedImageLabelValue},
			configSandboxImage: "k8s.gcr.io/pause",
			pullImageName:      "k8s.gcr.io/pause:latest",
		},
		{
			name:               "pinned image labels should get added on sandbox image specified with tag and digest both",
			expectedLabel:      map[string]string{labels.ImageLabelKey: labels.ImageLabelValue, labels.PinnedImageLabelKey: labels.PinnedImageLabelValue},
			configSandboxImage: "k8s.gcr.io/pause:3.9@sha256:45b23dee08af5e43a7fea6c4cf9c25ccf269ee113168c19722f87876677c5cb2",
			pullImageName:      "k8s.gcr.io/pause@sha256:45b23dee08af5e43a7fea6c4cf9c25ccf269ee113168c19722f87876677c5cb2",
		},

		{
			name:               "pinned image labels should get added on sandbox image specified with digest",
			expectedLabel:      map[string]string{labels.ImageLabelKey: labels.ImageLabelValue, labels.PinnedImageLabelKey: labels.PinnedImageLabelValue},
			configSandboxImage: "k8s.gcr.io/pause@sha256:45b23dee08af5e43a7fea6c4cf9c25ccf269ee113168c19722f87876677c5cb2",
			pullImageName:      "k8s.gcr.io/pause@sha256:45b23dee08af5e43a7fea6c4cf9c25ccf269ee113168c19722f87876677c5cb2",
		},

		{
			name:               "pinned image labels should not get added on other image",
			expectedLabel:      map[string]string{labels.ImageLabelKey: labels.ImageLabelValue},
			configSandboxImage: "k8s.gcr.io/pause:3.9",
			pullImageName:      "k8s.gcr.io/random:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			criService.config.SandboxImage = tt.configSandboxImage
			labels := criService.getLabels(context.Background(), tt.pullImageName)
			assert.Equal(t, tt.expectedLabel, labels)

		})
	}
}

func TestCreateImgcryptPayload(t *testing.T) {
	type args struct {
		podAnnotations map[string]string
	}
	tests := []struct {
		name     string
		args     args
		keyModel string
		want     *imgcrypt.Payload
	}{
		{
			name:     "node decryption model",
			args:     args{podAnnotations: map[string]string{}},
			keyModel: criconfig.KeyModelNode,
			want:     &imgcrypt.Payload{},
		},
		{
			name: "no key model",
			want: nil,
		},
		{
			name: "pod decryption model",
			args: args{podAnnotations: map[string]string{
				annotations.PodImageDecryptionConfig: "provider:test-crypt:12345",
			}},
			keyModel: criconfig.KeyModelPod,
			want: &imgcrypt.Payload{
				DecryptConfig: encconfig.DecryptConfig{Parameters: map[string][][]byte{
					"test-crypt": {
						[]byte("12345"),
					}}},
			},
		},
	}
	for _, tt := range tests {
		criService := newTestCRIService()
		t.Run(tt.name, func(t *testing.T) {
			criService.config.ImageDecryption.KeyModel = tt.keyModel
			assert.Equalf(t, tt.want, criService.createImgcryptPayload(tt.args.podAnnotations), "createImgcryptPayload(%v)", tt.args.podAnnotations)
		})
	}
}
