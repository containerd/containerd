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

package images

import (
	"context"
	"encoding/base64"
	"testing"

	docker "github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/internal/cri/annotations"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/containerd/containerd/v2/internal/cri/labels"
	"github.com/containerd/platforms"
)

func TestParseAuth(t *testing.T) {
	testUser := "username"
	testPasswd := "password"
	testAuthLen := base64.StdEncoding.EncodedLen(len(testUser + ":" + testPasswd))
	testAuth := make([]byte, testAuthLen)
	base64.StdEncoding.Encode(testAuth, []byte(testUser+":"+testPasswd))
	invalidAuth := make([]byte, testAuthLen)
	base64.StdEncoding.Encode(invalidAuth, []byte(testUser+"@"+testPasswd))
	for _, test := range []struct {
		desc           string
		auth           *runtime.AuthConfig
		host           string
		expectedUser   string
		expectedSecret string
		expectErr      bool
	}{
		{
			desc: "should not return error if auth config is nil",
		},
		{
			desc:      "should not return error if empty auth is provided for access to anonymous registry",
			auth:      &runtime.AuthConfig{},
			expectErr: false,
		},
		{
			desc:           "should support identity token",
			auth:           &runtime.AuthConfig{IdentityToken: "abcd"},
			expectedSecret: "abcd",
		},
		{
			desc: "should support username and password",
			auth: &runtime.AuthConfig{
				Username: testUser,
				Password: testPasswd,
			},
			expectedUser:   testUser,
			expectedSecret: testPasswd,
		},
		{
			desc:           "should support auth",
			auth:           &runtime.AuthConfig{Auth: string(testAuth)},
			expectedUser:   testUser,
			expectedSecret: testPasswd,
		},
		{
			desc:      "should return error for invalid auth",
			auth:      &runtime.AuthConfig{Auth: string(invalidAuth)},
			expectErr: true,
		},
		{
			desc: "should return empty auth if server address doesn't match",
			auth: &runtime.AuthConfig{
				Username:      testUser,
				Password:      testPasswd,
				ServerAddress: "https://registry-1.io",
			},
			host:           "registry-2.io",
			expectedUser:   "",
			expectedSecret: "",
		},
		{
			desc: "should return auth if server address matches",
			auth: &runtime.AuthConfig{
				Username:      testUser,
				Password:      testPasswd,
				ServerAddress: "https://registry-1.io",
			},
			host:           "registry-1.io",
			expectedUser:   testUser,
			expectedSecret: testPasswd,
		},
		{
			desc: "should return auth if server address is not specified",
			auth: &runtime.AuthConfig{
				Username: testUser,
				Password: testPasswd,
			},
			host:           "registry-1.io",
			expectedUser:   testUser,
			expectedSecret: testPasswd,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			u, s, err := ParseAuth(test.auth, test.host)
			assert.Equal(t, test.expectErr, err != nil)
			assert.Equal(t, test.expectedUser, u)
			assert.Equal(t, test.expectedSecret, s)
		})
	}
}

func TestRegistryEndpoints(t *testing.T) {
	for _, test := range []struct {
		desc     string
		mirrors  map[string]criconfig.Mirror
		host     string
		expected []string
	}{
		{
			desc: "no mirror configured",
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
		{
			desc: "mirror configured",
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
		{
			desc: "wildcard mirror configured",
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
		{
			desc: "host should take precedence if both host and wildcard mirrors are configured",
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
		{
			desc: "default endpoint in list with http",
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
		{
			desc: "default endpoint in list with https",
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
		{
			desc: "default endpoint in list with path",
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
		{
			desc: "miss scheme endpoint in list with path",
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
		test := test
		t.Run(test.desc, func(t *testing.T) {
			c, _ := newTestCRIService()
			c.config.Registry.Mirrors = test.mirrors
			got, err := c.registryEndpoints(test.host)
			assert.NoError(t, err)
			assert.Equal(t, test.expected, got)
		})
	}
}

func TestDefaultScheme(t *testing.T) {
	for _, test := range []struct {
		desc     string
		host     string
		expected string
	}{
		{
			desc:     "should use http by default for localhost",
			host:     "localhost",
			expected: "http",
		},
		{
			desc:     "should use http by default for localhost with port",
			host:     "localhost:8080",
			expected: "http",
		},
		{
			desc:     "should use http by default for 127.0.0.1",
			host:     "127.0.0.1",
			expected: "http",
		},
		{
			desc:     "should use http by default for 127.0.0.1 with port",
			host:     "127.0.0.1:8080",
			expected: "http",
		},
		{
			desc:     "should use http by default for ::1",
			host:     "::1",
			expected: "http",
		},
		{
			desc:     "should use http by default for ::1 with port",
			host:     "[::1]:8080",
			expected: "http",
		},
		{
			desc:     "should use https by default for remote host",
			host:     "remote",
			expected: "https",
		},
		{
			desc:     "should use https by default for remote host with port",
			host:     "remote:8080",
			expected: "https",
		},
		{
			desc:     "should use https by default for remote ip",
			host:     "8.8.8.8",
			expected: "https",
		},
		{
			desc:     "should use https by default for remote ip with port",
			host:     "8.8.8.8:8080",
			expected: "https",
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			got := defaultScheme(test.host)
			assert.Equal(t, test.expected, got)
		})
	}
}

// Temporarily remove for v2 upgrade
//func TestEncryptedImagePullOpts(t *testing.T) {
//	for _, test := range []struct {
//		desc         string
//		keyModel     string
//		expectedOpts int
//	}{
//		{
//			desc:         "node key model should return one unpack opt",
//			keyModel:     criconfig.KeyModelNode,
//			expectedOpts: 1,
//		},
//		{
//			desc:         "no key model selected should default to node key model",
//			keyModel:     "",
//			expectedOpts: 0,
//		},
//	} {
//		test := test
//		t.Run(test.desc, func(t *testing.T) {
//			c, _ := newTestCRIService()
//			c.config.ImageDecryption.KeyModel = test.keyModel
//			got := len(c.encryptedImagesPullOpts())
//			assert.Equal(t, test.expectedOpts, got)
//		})
//	}
//}

func TestSnapshotterFromPodSandboxConfig(t *testing.T) {
	defaultSnapshotter := "native"
	runtimeSnapshotter := "devmapper"
	tests := []struct {
		desc                string
		podSandboxConfig    *runtime.PodSandboxConfig
		expectedSnapshotter string
		expectedErr         bool
	}{
		{
			desc:                "should return default snapshotter for nil podSandboxConfig",
			expectedSnapshotter: defaultSnapshotter,
		},
		{
			desc:                "should return default snapshotter for nil podSandboxConfig.Annotations",
			podSandboxConfig:    &runtime.PodSandboxConfig{},
			expectedSnapshotter: defaultSnapshotter,
		},
		{
			desc: "should return default snapshotter for empty podSandboxConfig.Annotations",
			podSandboxConfig: &runtime.PodSandboxConfig{
				Annotations: make(map[string]string),
			},
			expectedSnapshotter: defaultSnapshotter,
		},
		{
			desc: "should return default snapshotter for runtime not found",
			podSandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.RuntimeHandler: "runtime-not-exists",
				},
			},
			expectedSnapshotter: defaultSnapshotter,
		},
		{
			desc: "should return snapshotter provided in podSandboxConfig.Annotations",
			podSandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.RuntimeHandler: "existing-runtime",
				},
			},
			expectedSnapshotter: runtimeSnapshotter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			cri, _ := newTestCRIService()
			cri.config.Snapshotter = defaultSnapshotter
			cri.runtimePlatforms["existing-runtime"] = ImagePlatform{
				Platform:    platforms.DefaultSpec(),
				Snapshotter: runtimeSnapshotter,
			}
			snapshotter, err := cri.snapshotterFromPodSandboxConfig(context.Background(), "test-image", tt.podSandboxConfig)
			assert.Equal(t, tt.expectedSnapshotter, snapshotter)
			if tt.expectedErr {
				assert.Error(t, err)
			}
		})
	}
}

func TestGetRepoDigestAndTag(t *testing.T) {
	digest := digest.Digest("sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582")
	for _, test := range []struct {
		desc               string
		ref                string
		schema1            bool
		expectedRepoDigest string
		expectedRepoTag    string
	}{
		{
			desc:               "repo tag should be empty if original ref has no tag",
			ref:                "gcr.io/library/busybox@" + digest.String(),
			expectedRepoDigest: "gcr.io/library/busybox@" + digest.String(),
		},
		{
			desc:               "repo tag should not be empty if original ref has tag",
			ref:                "gcr.io/library/busybox:latest",
			expectedRepoDigest: "gcr.io/library/busybox@" + digest.String(),
			expectedRepoTag:    "gcr.io/library/busybox:latest",
		},
		{
			desc:               "repo digest should be empty if original ref is schema1 and has no digest",
			ref:                "gcr.io/library/busybox:latest",
			schema1:            true,
			expectedRepoDigest: "",
			expectedRepoTag:    "gcr.io/library/busybox:latest",
		},
		{
			desc:               "repo digest should not be empty if original ref is schema1 but has digest",
			ref:                "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59594",
			schema1:            true,
			expectedRepoDigest: "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59594",
			expectedRepoTag:    "",
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			named, err := docker.ParseDockerRef(test.ref)
			assert.NoError(t, err)
			repoDigest, repoTag := getRepoDigestAndTag(named, digest, test.schema1)
			assert.Equal(t, test.expectedRepoDigest, repoDigest)
			assert.Equal(t, test.expectedRepoTag, repoTag)
		})
	}
}

func TestImageGetLabels(t *testing.T) {

	criService, _ := newTestCRIService()

	tests := []struct {
		name          string
		expectedLabel map[string]string
		pinnedImages  map[string]string
		pullImageName string
	}{
		{
			name:          "pinned image labels should get added on sandbox image",
			expectedLabel: map[string]string{labels.ImageLabelKey: labels.ImageLabelValue, labels.PinnedImageLabelKey: labels.PinnedImageLabelValue},
			pinnedImages:  map[string]string{"sandbox": "k8s.gcr.io/pause:3.9"},
			pullImageName: "k8s.gcr.io/pause:3.9",
		},
		{
			name:          "pinned image labels should get added on sandbox image without tag",
			expectedLabel: map[string]string{labels.ImageLabelKey: labels.ImageLabelValue, labels.PinnedImageLabelKey: labels.PinnedImageLabelValue},
			pinnedImages:  map[string]string{"sandboxnotag": "k8s.gcr.io/pause", "sandbox": "k8s.gcr.io/pause:latest"},
			pullImageName: "k8s.gcr.io/pause:latest",
		},
		{
			name:          "pinned image labels should get added on sandbox image specified with tag and digest both",
			expectedLabel: map[string]string{labels.ImageLabelKey: labels.ImageLabelValue, labels.PinnedImageLabelKey: labels.PinnedImageLabelValue},
			pinnedImages: map[string]string{
				"sandboxtagdigest": "k8s.gcr.io/pause:3.9@sha256:45b23dee08af5e43a7fea6c4cf9c25ccf269ee113168c19722f87876677c5cb2",
				"sandbox":          "k8s.gcr.io/pause@sha256:45b23dee08af5e43a7fea6c4cf9c25ccf269ee113168c19722f87876677c5cb2",
			},
			pullImageName: "k8s.gcr.io/pause@sha256:45b23dee08af5e43a7fea6c4cf9c25ccf269ee113168c19722f87876677c5cb2",
		},

		{
			name:          "pinned image labels should get added on sandbox image specified with digest",
			expectedLabel: map[string]string{labels.ImageLabelKey: labels.ImageLabelValue, labels.PinnedImageLabelKey: labels.PinnedImageLabelValue},
			pinnedImages:  map[string]string{"sandbox": "k8s.gcr.io/pause@sha256:45b23dee08af5e43a7fea6c4cf9c25ccf269ee113168c19722f87876677c5cb2"},
			pullImageName: "k8s.gcr.io/pause@sha256:45b23dee08af5e43a7fea6c4cf9c25ccf269ee113168c19722f87876677c5cb2",
		},

		{
			name:          "pinned image labels should not get added on other image",
			expectedLabel: map[string]string{labels.ImageLabelKey: labels.ImageLabelValue},
			pinnedImages:  map[string]string{"sandbox": "k8s.gcr.io/pause:3.9"},
			pullImageName: "k8s.gcr.io/random:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			criService.config.PinnedImages = tt.pinnedImages
			labels := criService.getLabels(context.Background(), tt.pullImageName)
			assert.Equal(t, tt.expectedLabel, labels)

		})
	}
}
