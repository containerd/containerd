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
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	ctrdimages "github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/transfer"
	transferimage "github.com/containerd/containerd/v2/core/transfer/image"
	"github.com/containerd/containerd/v2/internal/cri/annotations"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/containerd/containerd/v2/internal/cri/labels"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
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
		t.Run(test.desc, func(t *testing.T) {
			got := defaultScheme(test.host)
			assert.Equal(t, test.expected, got)
		})
	}
}

// Temporarily remove for v2 upgrade
func TestEncryptedImagePullOpts(t *testing.T) {
	for _, test := range []struct {
		desc         string
		keyModel     string
		expectedOpts int
	}{
		{
			desc:         "node key model should return one unpack opt",
			keyModel:     criconfig.KeyModelNode,
			expectedOpts: 1,
		},
		{
			desc:         "no key model selected should default to node key model",
			keyModel:     "",
			expectedOpts: 0,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			c, _ := newTestCRIService()
			c.config.ImageDecryption.KeyModel = test.keyModel
			got := len(c.encryptedImagesPullOpts())
			assert.Equal(t, test.expectedOpts, got)
		})
	}
}

func TestResolveRequestRuntimeHandler(t *testing.T) {
	defaultSnapshotter := "native"
	runtimeSnapshotter := "devmapper"
	runtimePlatform := ocispec.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}
	tests := []struct {
		desc                string
		podSandboxConfig    *runtime.PodSandboxConfig
		runtimeHandler      string
		defaultRuntimeName  string
		expectedSnapshotter string
		expectedPlatform    *ocispec.Platform
		expectedErr         bool
	}{
		{
			desc:                "should use default runtime handler for nil podSandboxConfig",
			expectedSnapshotter: defaultSnapshotter,
		},
		{
			desc:                "should use default runtime handler for empty runtimeHandler",
			podSandboxConfig:    &runtime.PodSandboxConfig{},
			expectedSnapshotter: defaultSnapshotter,
		},
		{
			desc:                "should use snapshotter for existing runtime",
			podSandboxConfig:    &runtime.PodSandboxConfig{},
			runtimeHandler:      "existing-runtime",
			expectedSnapshotter: runtimeSnapshotter,
		},
		{
			desc:                "should honor explicit runtime handler with nil podSandboxConfig",
			runtimeHandler:      "existing-runtime",
			expectedSnapshotter: runtimeSnapshotter,
		},
		{
			desc:                "should use platform configured for the runtime handler",
			runtimeHandler:      "platform-runtime",
			expectedSnapshotter: runtimeSnapshotter,
			expectedPlatform:    &runtimePlatform,
		},
		{
			desc:                "should resolve default runtime handler without configured platform",
			podSandboxConfig:    &runtime.PodSandboxConfig{},
			defaultRuntimeName:  "existing-runtime",
			expectedSnapshotter: runtimeSnapshotter,
		},
		{
			desc:                "should resolve default runtime handler with configured platform",
			podSandboxConfig:    &runtime.PodSandboxConfig{},
			defaultRuntimeName:  "platform-runtime",
			expectedSnapshotter: runtimeSnapshotter,
			expectedPlatform:    &runtimePlatform,
		},
		{
			desc:             "should reject unknown runtime handler",
			podSandboxConfig: &runtime.PodSandboxConfig{},
			runtimeHandler:   "runtime-not-exists",
			expectedErr:      true,
		},
		{
			desc: "should fall back to annotation when runtimeHandler is empty",
			podSandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.RuntimeHandler: "existing-runtime",
				},
			},
			expectedSnapshotter: runtimeSnapshotter,
		},
		{
			desc: "should reject unknown runtime handler from annotation",
			podSandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.RuntimeHandler: "runtime-not-exists",
				},
			},
			expectedErr: true,
		},
		{
			desc:           "should prefer runtimeHandler parameter over annotation",
			runtimeHandler: "platform-runtime",
			podSandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.RuntimeHandler: "runtime-not-exists",
				},
			},
			expectedSnapshotter: runtimeSnapshotter,
			expectedPlatform:    &runtimePlatform,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			cri, _ := newTestCRIService()
			cri.config.Snapshotter = defaultSnapshotter
			cri.UpdateDefaultRuntimeName(tt.defaultRuntimeName)
			cri.runtimePlatforms["existing-runtime"] = &ImagePlatform{
				Snapshotter: runtimeSnapshotter,
			}
			cri.runtimePlatforms["platform-runtime"] = &ImagePlatform{
				Platform:    &runtimePlatform,
				Snapshotter: runtimeSnapshotter,
			}
			h, err := cri.resolveRequestRuntimeHandler(context.Background(), tt.podSandboxConfig, tt.runtimeHandler)
			if tt.expectedErr {
				assert.ErrorIs(t, err, errdefs.ErrInvalidArgument)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedSnapshotter, h.Snapshotter)
			assert.Equal(t, tt.expectedPlatform, h.Platform)
		})
	}
}

func TestPullImageRuntimeHandler(t *testing.T) {
	cri, _ := newTestCRIService()

	_, err := cri.PullImage(context.Background(), "test-image", nil, nil, "runtime-not-exists")
	assert.ErrorIs(t, err, errdefs.ErrInvalidArgument)
}

func TestRuntimeHandlerPullBackends(t *testing.T) {
	platform := ocispec.Platform{
		OS:           "windows",
		Architecture: "amd64",
		OSVersion:    "10.0.20348",
		OSFeatures:   []string{"win32k"},
	}

	t.Run("local pull", func(t *testing.T) {
		client := &pullTestClient{
			pull: func(_ context.Context, _ string, opts ...containerd.RemoteOpt) (containerd.Image, error) {
				remote := &containerd.RemoteContext{}
				for _, opt := range opts {
					require.NoError(t, opt(nil, remote))
				}
				require.Equal(t, "native", remote.Snapshotter)
				require.True(t, remote.PlatformMatcher.Match(platform))
				return nil, errdefs.ErrUnavailable
			},
		}
		c := &CRIImageService{
			client: client,
			config: criconfig.ImageConfig{DisableSnapshotAnnotations: true},
		}

		_, _, err := c.pullImageWithLocalPull(context.Background(), "example.com/test:latest", nil, "native", &platform, nil, time.Minute)
		require.ErrorIs(t, err, errdefs.ErrUnavailable)
	})

	t.Run("transfer pull", func(t *testing.T) {
		transferrer := &pullTestTransferrer{
			transfer: func(_ context.Context, _, destination any, _ ...transfer.Opt) error {
				store := destination.(*transferimage.Store)
				require.Equal(t, []ocispec.Platform{platform}, store.Platforms())
				require.Equal(t, platform, store.UnpackPlatforms()[0].Platform)
				require.Equal(t, "native", store.UnpackPlatforms()[0].Snapshotter)
				return nil
			},
		}
		client := &pullTestClient{
			getImageWithPlatform: func(_ context.Context, ref string, matcher platforms.MatchComparer) (containerd.Image, error) {
				require.True(t, matcher.Match(platform))
				return containerd.NewImageWithPlatform(&containerd.Client{}, ctrdimages.Image{Name: ref}, matcher), nil
			},
		}
		c := &CRIImageService{client: client, transferrer: transferrer}

		image, _, err := c.pullImageWithTransferService(context.Background(), "example.com/test:latest", nil, "native", &platform, nil, time.Minute)
		require.NoError(t, err)
		require.True(t, image.Platform().Match(platform))
	})

	t.Run("unsupported unpack", func(t *testing.T) {
		localCalled := false
		client := &pullTestClient{
			pull: func(_ context.Context, _ string, opts ...containerd.RemoteOpt) (containerd.Image, error) {
				localCalled = true
				remote := &containerd.RemoteContext{}
				for _, opt := range opts {
					require.NoError(t, opt(nil, remote))
				}
				require.Equal(t, platforms.FormatAll(platform), remote.Labels[labels.ImagePlatformLabelKey])
				return nil, errdefs.ErrUnavailable
			},
		}
		transferrer := &pullTestTransferrer{
			supportsUnpack: func(p ocispec.Platform, snapshotter string) bool {
				require.Equal(t, platform, p)
				require.Equal(t, "native", snapshotter)
				return false
			},
			transfer: func(context.Context, any, any, ...transfer.Opt) error {
				t.Fatal("transfer should not be called")
				return nil
			},
		}
		c := &CRIImageService{
			client:           client,
			transferrer:      transferrer,
			runtimePlatforms: map[string]*ImagePlatform{"native": {Snapshotter: "native", Platform: &platform}},
			config:           criconfig.ImageConfig{ImagePullProgressTimeout: "1m", DisableSnapshotAnnotations: true},
		}

		_, err := c.PullImage(context.Background(), "example.com/test:latest", nil, nil, "native")
		require.ErrorIs(t, err, errdefs.ErrUnavailable)
		require.True(t, localCalled)
	})
}

func TestPersistedImagePlatform(t *testing.T) {
	platform := ocispec.Platform{
		OS:           "windows",
		Architecture: "amd64",
		OSVersion:    "10.0.20348",
		OSFeatures:   []string{"win32k"},
	}
	ref := "example.com/test:latest"
	image := containerd.NewImage(&containerd.Client{}, ctrdimages.Image{
		Name:   ref,
		Labels: map[string]string{labels.ImagePlatformLabelKey: platforms.FormatAll(platform)},
	})

	t.Run("event update", func(t *testing.T) {
		client := &pullTestClient{
			getImage: func(context.Context, string) (containerd.Image, error) {
				return image, nil
			},
			getImageWithPlatform: func(_ context.Context, _ string, matcher platforms.MatchComparer) (containerd.Image, error) {
				require.True(t, matcher.Match(platform))
				return nil, errdefs.ErrUnavailable
			},
		}
		c := &CRIImageService{client: client}
		require.ErrorIs(t, c.UpdateImage(context.Background(), ref), errdefs.ErrUnavailable)
	})

	t.Run("recovery", func(t *testing.T) {
		called := false
		client := &pullTestClient{
			listImages: func(context.Context, ...string) ([]containerd.Image, error) {
				return []containerd.Image{image}, nil
			},
			getImageWithPlatform: func(_ context.Context, _ string, matcher platforms.MatchComparer) (containerd.Image, error) {
				called = true
				require.True(t, matcher.Match(platform))
				return nil, errdefs.ErrUnavailable
			},
		}
		c := &CRIImageService{client: client}
		require.NoError(t, c.CheckImages(context.Background()))
		require.True(t, called)
	})
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
			pinnedImages:  map[string]string{"sandbox": "registry.k8s.io/pause:3.10.2"},
			pullImageName: "registry.k8s.io/pause:3.10.2",
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

func TestTransferProgressReporter(t *testing.T) {

	tests := []struct {
		name     string
		setup    func(*transferProgressReporter) chan struct{}
		progress []transfer.Progress
		check    func(*testing.T, *transferProgressReporter, <-chan struct{})
	}{
		{
			name: "PullImageWithCompleteEvent",
			progress: []transfer.Progress{
				{
					Name: "layer1",
					Desc: &ocispec.Descriptor{
						MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
						Digest:    "sha256:abcdef",
						Size:      1000,
					},
					Total:    1000,
					Progress: 500,
					Event:    "downloading",
				},
				{
					Name: "layer1",
					Desc: &ocispec.Descriptor{
						MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
						Digest:    "sha256:abcdef",
						Size:      1000,
					},
					Total:    1000,
					Progress: 1000,
					Event:    "complete",
				},
			},
			check: func(t *testing.T, r *transferProgressReporter, cancelCalled <-chan struct{}) {
				activeReqs, totalBytesRead := r.reqReporter.status()
				assert.Equal(t, int32(0), activeReqs, "Expected 0 active requests")
				assert.Equal(t, uint64(1000), totalBytesRead, "Expected 1000 bytes read")
			},
		},
		{
			name: "FinishedDownloadingWithNoCompleteEvent",
			progress: []transfer.Progress{
				{
					Name: "layer1",
					Desc: &ocispec.Descriptor{
						MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
						Digest:    "sha256:abcdef",
						Size:      1000,
					},
					Total:    1000,
					Progress: 500,
					Event:    "downloading",
				},
				{
					Name: "layer1",
					Desc: &ocispec.Descriptor{
						MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
						Digest:    "sha256:abcdef",
						Size:      1000,
					},
					Total:    1000,
					Progress: 1000,
					Event:    "downloading",
				},
			},
			check: func(t *testing.T, r *transferProgressReporter, cancelCalled <-chan struct{}) {
				activeReqs, totalBytesRead := r.reqReporter.status()
				assert.Equal(t, int32(0), activeReqs, "Expected 0 active requests")
				assert.Equal(t, uint64(1000), totalBytesRead, "Expected 1000 bytes read")
			},
		},
		{
			name: "NilDescriptorInProgressNode",
			progress: []transfer.Progress{
				{
					Name:     "layer1",
					Total:    1000,
					Progress: 500,
					Event:    "downloading",
				},
			},
			check: func(t *testing.T, r *transferProgressReporter, cancelCalled <-chan struct{}) {
				assert.Equal(t, int32(0), r.reqReporter.activeReqs.Load(), "Expected zero active request")
				assert.Equal(t, uint64(0), r.reqReporter.totalBytesRead.Load(), "Expected zero bytes read")
			},
		},
		{
			name: "EmptyTotalInProgressNode",
			progress: []transfer.Progress{
				{
					Name: "layer1",
					Desc: &ocispec.Descriptor{
						MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
						Digest:    "sha256:abcdef",
						Size:      1000,
					},
					Total:    0,
					Progress: 500,
					Event:    "downloading",
				},
			},
			check: func(t *testing.T, r *transferProgressReporter, cancelCalled <-chan struct{}) {
				activeReqs, totalBytesRead := r.reqReporter.status()
				assert.Equal(t, int32(0), activeReqs, "Expected zero active request")
				assert.Equal(t, uint64(0), totalBytesRead, "Expected zero bytes read")
			},
		},
		{
			name: "TimeoutDuringPull",
			setup: func(r *transferProgressReporter) chan struct{} {
				r.timeout = 100 * time.Millisecond

				cancelCalled := make(chan struct{})
				originalCancel := r.cancel
				r.cancel = func() {
					originalCancel()
					close(cancelCalled)
				}

				return cancelCalled
			},
			progress: []transfer.Progress{
				{
					Name: "layer1",
					Desc: &ocispec.Descriptor{
						MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
						Digest:    "sha256:abcdef",
						Size:      1000,
					},
					Total:    1000,
					Progress: 500,
					Event:    "downloading",
				},
				{
					Name: "layer1",
					Desc: &ocispec.Descriptor{
						MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
						Digest:    "sha256:abcdef",
						Size:      1000,
					},
					Total:    1000,
					Progress: 500,
					Event:    "downloading",
				},
				{
					Name: "layer1",
					Desc: &ocispec.Descriptor{
						MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
						Digest:    "sha256:abcdef",
						Size:      1000,
					},
					Total:    1000,
					Progress: 500,
					Event:    "downloading",
				},
			},
			check: func(t *testing.T, r *transferProgressReporter, cancelCalled <-chan struct{}) {
				select {
				case <-cancelCalled:
					// Expected behavior: cancel was called
				case <-time.After(150 * time.Millisecond):
					t.Error("Cancel function was not called within the expected timeframe")
				}
			},
		},
		{
			name: "MultipleRequests",
			progress: []transfer.Progress{
				{
					Name: "layer1",
					Desc: &ocispec.Descriptor{
						MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
						Digest:    "sha256:abcdef1",
						Size:      1000,
					},
					Total:    1000,
					Progress: 500,
					Event:    "downloading",
				},
				{
					Name: "layer2",
					Desc: &ocispec.Descriptor{
						MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
						Digest:    "sha256:abcdef2",
						Size:      2000,
					},
					Total:    2000,
					Progress: 1000,
					Event:    "downloading",
				},
				{
					Name: "layer1",
					Desc: &ocispec.Descriptor{
						MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
						Digest:    "sha256:abcdef1",
						Size:      1000,
					},
					Total:    1000,
					Progress: 1000,
					Event:    "complete",
				},
			},
			check: func(t *testing.T, r *transferProgressReporter, cancelCalled <-chan struct{}) {
				activeReqs, totalBytesRead := r.reqReporter.status()
				assert.Equal(t, int32(1), activeReqs, "Expected one active request")
				assert.Equal(t, uint64(2000), totalBytesRead, "Expected 2000 bytes read")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			reporter := &transferProgressReporter{
				reqReporter: pullRequestReporter{},
				pc:          make(chan transfer.Progress),
				statuses:    make(map[string]*transfer.Progress),
				ref:         "test-image:latest",
				timeout:     30 * time.Second,
				cancel:      cancel,
			}

			var cancelCalled chan struct{}
			if tt.setup != nil {
				cancelCalled = tt.setup(reporter)
			}

			go reporter.start(ctx)

			for _, progress := range tt.progress {
				reporter.pc <- progress
				time.Sleep(50 * time.Millisecond) // Allow some time for processing
			}

			if tt.check != nil {
				tt.check(t, reporter, cancelCalled)
			}
		})
	}
}

// TestPullProgressReporter covers the core no-progress cancellation
// behavior of pullProgressReporter: a stuck request (active, no bytes)
// is eventually cancelled, while a progressing request is not.
//
// The flaky failure in TestCRIImagePullTimeout/HoldingContentOpenWriterWithLocalPull
// — which this fix addresses — is a timing race that's not cleanly
// expressible as a unit test: the boundary between the buggy and fixed
// cancel times coincides at 1.5*timeout, so any check near that
// threshold is scheduler-jitter prone. The semantic regression is
// covered by the existing integration test.
func TestPullProgressReporter(t *testing.T) {
	t.Run("StuckRequestStillGetsCancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		cancelCalled := make(chan struct{})
		reporter := newPullProgressReporter("test-image:latest", func() {
			select {
			case <-cancelCalled:
			default:
				close(cancelCalled)
			}
		}, 200*time.Millisecond)

		// Start a request immediately (no idle period) and never produce
		// bytes. The reporter must cancel after timeout elapses.
		reporter.reqReporter.incRequest()
		reporter.start(ctx)

		select {
		case <-cancelCalled:
		case <-time.After(2 * time.Second):
			t.Fatal("stuck request was not cancelled")
		}
	})

	t.Run("ProgressingRequestIsNotCancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		cancelCalled := make(chan struct{})
		reporter := newPullProgressReporter("test-image:latest", func() {
			select {
			case <-cancelCalled:
			default:
				close(cancelCalled)
			}
		}, 200*time.Millisecond)

		reporter.reqReporter.incRequest()
		reporter.start(ctx)

		// Advance bytes faster than timeout so the reporter keeps
		// refreshing lastSeenBytesRead.
		done := make(chan struct{})
		go func() {
			defer close(done)
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for i := 0; i < 10; i++ {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					reporter.reqReporter.incByteRead(1024)
				}
			}
		}()

		select {
		case <-cancelCalled:
			t.Fatal("pull was cancelled despite making byte progress")
		case <-done:
		}
	})
}

type pullTestClient struct {
	listImages           func(context.Context, ...string) ([]containerd.Image, error)
	getImage             func(context.Context, string) (containerd.Image, error)
	pull                 func(context.Context, string, ...containerd.RemoteOpt) (containerd.Image, error)
	getImageWithPlatform func(context.Context, string, platforms.MatchComparer) (containerd.Image, error)
}

func (c *pullTestClient) ListImages(ctx context.Context, filters ...string) ([]containerd.Image, error) {
	if c.listImages != nil {
		return c.listImages(ctx, filters...)
	}
	return nil, nil
}

func (c *pullTestClient) GetImage(ctx context.Context, ref string) (containerd.Image, error) {
	if c.getImage != nil {
		return c.getImage(ctx, ref)
	}
	return nil, errdefs.ErrNotImplemented
}

func (c *pullTestClient) GetImageWithPlatform(ctx context.Context, ref string, platform platforms.MatchComparer) (containerd.Image, error) {
	return c.getImageWithPlatform(ctx, ref, platform)
}

func (c *pullTestClient) Pull(ctx context.Context, ref string, opts ...containerd.RemoteOpt) (containerd.Image, error) {
	return c.pull(ctx, ref, opts...)
}

type pullTestTransferrer struct {
	transfer       func(context.Context, any, any, ...transfer.Opt) error
	supportsUnpack func(ocispec.Platform, string) bool
}

func (t *pullTestTransferrer) Transfer(ctx context.Context, source, destination any, opts ...transfer.Opt) error {
	return t.transfer(ctx, source, destination, opts...)
}

func (t *pullTestTransferrer) SupportsUnpack(_ context.Context, platform ocispec.Platform, snapshotter string) bool {
	return t.supportsUnpack(platform, snapshotter)
}

// fakeObserver records every Observe call so tests can assert both the
// presence and the value of observations without touching the real histogram.
type fakeObserver struct {
	samples []float64
}

func (f *fakeObserver) Observe(v float64) {
	f.samples = append(f.samples, v)
}

func TestRecordImagePullThroughput(t *testing.T) {
	for _, tc := range []struct {
		name        string
		bytesPulled uint64
		duration    time.Duration
		wantSamples int
		wantValue   float64 // only checked when wantSamples == 1
	}{
		{
			name:        "fully cached pull is not observed",
			bytesPulled: 0,
			duration:    2 * time.Second,
			wantSamples: 0,
		},
		{
			name:        "zero duration is not observed",
			bytesPulled: 10 * mibToByte,
			duration:    0,
			wantSamples: 0,
		},
		{
			name:        "cold pull observes MiB/s from fetched bytes",
			bytesPulled: 10 * mibToByte,
			duration:    2 * time.Second,
			wantSamples: 1,
			wantValue:   5.0,
		},
		{
			name: "partial cache hit observes only fetched bytes",
			// 200 MiB image, 150 MiB cached, 50 MiB actually fetched over 1s.
			bytesPulled: 50 * mibToByte,
			duration:    1 * time.Second,
			wantSamples: 1,
			wantValue:   50.0,
		},
		{
			name:        "sub-second pull observes correctly",
			bytesPulled: 25 * mibToByte,
			duration:    500 * time.Millisecond,
			wantSamples: 1,
			wantValue:   50.0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obs := &fakeObserver{}
			recordImagePullThroughput(obs, tc.bytesPulled, tc.duration)
			if assert.Len(t, obs.samples, tc.wantSamples) && tc.wantSamples == 1 {
				assert.InDelta(t, tc.wantValue, obs.samples[0], 0.001)
			}
		})
	}
}
