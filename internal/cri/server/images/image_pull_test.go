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
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	eventstypes "github.com/containerd/containerd/api/events"
	transfertypes "github.com/containerd/containerd/api/types/transfer"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/events"
	"github.com/containerd/containerd/v2/core/events/exchange"
	ctrdimages "github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/imagetest"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/metadata/boltutil"
	"github.com/containerd/containerd/v2/core/transfer"
	"github.com/containerd/containerd/v2/internal/cri/annotations"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/containerd/containerd/v2/internal/cri/labels"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	"github.com/containerd/containerd/v2/internal/kmutex"
	"github.com/containerd/containerd/v2/pkg/gc"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	bolt "go.etcd.io/bbolt"
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
			desc: "should ignore unknown runtime handler from annotation",
			podSandboxConfig: &runtime.PodSandboxConfig{
				Annotations: map[string]string{
					annotations.RuntimeHandler: "runtime-not-exists",
				},
			},
			expectedSnapshotter: defaultSnapshotter,
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

	t.Run("default runtime ownership", func(t *testing.T) {
		ctx, c, cs := newImageEventService(t)
		ctx = leases.WithLease(ctx, "pull")
		c.defaultRuntimeName = "runc"
		c.runtimePlatforms["runc"] = &ImagePlatform{Platform: &platform}
		c.config.ImagePullProgressTimeout = "1m"
		config := cs.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{Platform: platform, RootFS: ocispec.RootFS{Type: "layers"}})
		manifest := cs.Manifest(config)
		c.transferrer = &pullTestTransferrer{supportsUnpack: func(ocispec.Platform, string) bool { return true }, transfer: func(ctx context.Context, _, destination any, _ ...transfer.Opt) error {
			_, err := destination.(transfer.ImageStorer).Store(ctx, manifest.Descriptor, c.images)
			return err
		}}
		ref := "example.com/test:latest"
		id, err := c.PullImage(ctx, ref, nil, nil, "")
		require.NoError(t, err)
		c.defaultRuntimeName = "other"
		c.runtimePlatforms["other"] = &ImagePlatform{}
		delete(c.runtimePlatforms, "runc")
		c.imageStore = imagestore.NewStore(c.images, cs.Store, platforms.Default())
		persisted, err := c.images.List(ctx)
		require.NoError(t, err)
		for _, image := range persisted {
			require.NoError(t, c.UpdateImage(ctx, image.Name))
		}
		image, err := c.LocalResolve(ref, "runc")
		require.NoError(t, err)
		require.Equal(t, id, image.ID)
		require.Equal(t, platform, image.ImageSpec.Platform)
		_, err = c.LocalResolve(ref, "other")
		require.ErrorIs(t, err, errdefs.ErrNotFound)
		stored, err := c.images.Get(ctx, imagestore.Name(ref, "runc"))
		require.NoError(t, err)
		called := false
		c.client = &pullTestClient{
			listImages: func(context.Context, ...string) ([]containerd.Image, error) {
				return []containerd.Image{containerd.NewImage(&containerd.Client{}, stored)}, nil
			},
			getImageWithPlatform: func(_ context.Context, _ string, matcher platforms.MatchComparer) (containerd.Image, error) {
				called = true
				require.True(t, matcher.Match(platform))
				return nil, errdefs.ErrUnavailable
			},
		}
		require.NoError(t, c.CheckImages(ctx))
		require.True(t, called)
		_, err = c.PullImage(ctx, ref, nil, nil, "runc")
		require.ErrorIs(t, err, errdefs.ErrInvalidArgument)
		require.NoError(t, c.RemoveImage(ctx, &runtime.ImageSpec{Image: ref, RuntimeHandler: "runc"}))
		_, err = c.images.Get(ctx, stored.Name)
		require.ErrorIs(t, err, errdefs.ErrNotFound)
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
				require.Equal(t, "native", remote.Snapshotter)
				require.Equal(t, imagestore.Name("example.com/test:latest", "native"), remote.ImageName)
				require.True(t, remote.PlatformMatcher.Match(platform))
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
			client:              client,
			transferrer:         transferrer,
			imageMutationLocker: kmutex.New(),
			runtimePlatforms:    map[string]*ImagePlatform{"native": {Snapshotter: "native", Platform: &platform}},
			config:              criconfig.ImageConfig{ImagePullProgressTimeout: "1m", DisableSnapshotAnnotations: true},
		}

		_, err := c.PullImage(context.Background(), "example.com/test:latest", nil, nil, "native")
		require.ErrorIs(t, err, errdefs.ErrUnavailable)
		require.True(t, localCalled)
	})
}

func newImageEventService(t *testing.T) (context.Context, *CRIImageService, imagetest.ContentStore) {
	t.Helper()
	ctx := namespaces.WithNamespace(context.Background(), "test")
	cs := imagetest.NewContentStore(ctx, t)
	db, err := bolt.Open(filepath.Join(t.TempDir(), "metadata.db"), 0o600, nil)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	mdb := metadata.NewDB(db, cs.Store, nil, metadata.WithEventsPublisher(exchange.NewExchange()))
	store := metadata.NewImageStore(mdb)
	client, err := containerd.New("", containerd.WithServices(containerd.WithImageStore(store), containerd.WithContentStore(cs.Store)))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	return ctx, &CRIImageService{
		images: store, content: cs.Store, metadata: mdb, client: &platformImageClient{client},
		runtimePlatforms:    map[string]*ImagePlatform{"runc": {}},
		imageStore:          imagestore.NewStore(store, cs.Store, platforms.Default()),
		imageMutationLocker: kmutex.New(),
	}, cs
}

type imageEventStore struct {
	ctrdimages.Store
	afterGet     func(context.Context, string)
	beforeUpdate func(context.Context)
	beforeDelete func(context.Context, string)
	beforeList   func(context.Context)
	updates      int
}

func (s *imageEventStore) Get(ctx context.Context, name string) (ctrdimages.Image, error) {
	image, err := s.Store.Get(ctx, name)
	if err == nil && s.afterGet != nil {
		after := s.afterGet
		s.afterGet = nil
		after(ctx, name)
	}
	return image, err
}

func (s *imageEventStore) List(ctx context.Context, filters ...string) ([]ctrdimages.Image, error) {
	images, err := s.Store.List(ctx, filters...)
	if err == nil && s.beforeList != nil {
		s.beforeList(ctx)
	}
	return images, err
}

func (s *imageEventStore) Delete(ctx context.Context, name string, opts ...ctrdimages.DeleteOpt) error {
	if s.beforeDelete != nil {
		s.beforeDelete(ctx, name)
	}
	return s.Store.Delete(ctx, name, opts...)
}

func (s *imageEventStore) Update(ctx context.Context, image ctrdimages.Image, fields ...string) (ctrdimages.Image, error) {
	s.updates++
	if s.beforeUpdate != nil {
		before := s.beforeUpdate
		s.beforeUpdate = nil
		before(ctx)
	}
	return s.Store.Update(ctx, image, fields...)
}

func TestImageLifecycleMutationLocks(t *testing.T) {
	ctx, c, cs := newImageEventService(t)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c.defaultRuntimeName = "runc"
	config := cs.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{Platform: platforms.DefaultSpec(), RootFS: ocispec.RootFS{Type: "layers"}})
	manifest := cs.Manifest(config)
	ref := imagestore.Name("example.com/test:latest", "runc")
	id := imagestore.Name(config.Descriptor.Digest.String(), "runc")
	_, err := c.images.Create(ctx, ctrdimages.Image{Name: ref, Target: manifest.Descriptor})
	require.NoError(t, err)
	store := &imageEventStore{Store: c.images}
	store.beforeUpdate = func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, time.Millisecond)
		defer cancel()
		err := c.imageMutationLocker.Lock(ctx, "runc")
		if err == nil {
			c.imageMutationLocker.Unlock("runc")
		}
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	}
	c.images = store
	subscriber := c.metadata.Publisher(ctx).(events.Subscriber)
	notifications, errors := subscriber.Subscribe(ctx, "event.name=="+id)
	for _, topic := range []string{"/images/create", "/images/update"} {
		if topic == "/images/update" {
			_, err := c.images.Update(ctx, ctrdimages.Image{Name: ref, Labels: map[string]string{labels.PinnedImageLabelKey: labels.PinnedImageLabelValue}}, "labels."+labels.PinnedImageLabelKey)
			require.NoError(t, err)
		}
		require.NoError(t, c.UpdateImage(ctx, ref))
		select {
		case notification := <-notifications:
			require.NotNil(t, notification)
			require.Equal(t, topic, notification.Topic)
			decoded, err := typeurl.UnmarshalAny(notification.Event)
			require.NoError(t, err)
			if topic == "/images/create" {
				require.IsType(t, &eventstypes.ImageCreate{}, decoded)
			} else {
				require.IsType(t, &eventstypes.ImageUpdate{}, decoded)
			}
		case err := <-errors:
			t.Fatalf("waiting for ID publication: %v", err)
		}
	}
	deleted := false
	store.beforeDelete = func(ctx context.Context, _ string) {
		tx, ok := boltutil.Transaction(ctx)
		require.True(t, ok)
		require.True(t, tx.Writable())
		deleted = true
		ctx, cancel := context.WithTimeout(ctx, time.Millisecond)
		defer cancel()
		err := c.imageMutationLocker.Lock(ctx, "runc")
		if err == nil {
			c.imageMutationLocker.Unlock("runc")
		}
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	}
	other := imagestore.Name("example.com/other:latest", "other")
	_, err = c.images.Create(ctx, ctrdimages.Image{Name: other, Target: manifest.Descriptor})
	require.NoError(t, err)
	collected := false
	events, errs := subscriber.Subscribe(ctx, `topic=="/images/delete"`)
	c.gc = func(ctx context.Context) (gc.Stats, error) {
		collected = true
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		published := make(chan error, 1)
		go func() { published <- c.UpdateImage(ctx, other) }()
		select {
		case err := <-published:
			assert.NoError(t, err)
		case <-ctx.Done():
			t.Error("removal blocked publication for another handler")
		}
		_, err := store.Get(ctx, ref)
		assert.ErrorIs(t, err, errdefs.ErrNotFound, "GC must run after reference deletion")
		return nil, errdefs.ErrUnavailable
	}
	require.ErrorIs(t, c.RemoveImage(ctx, &runtime.ImageSpec{Image: config.Descriptor.Digest.String(), RuntimeHandler: "runc"}), errdefs.ErrUnavailable)
	require.True(t, collected)
	require.True(t, deleted)
	_, err = store.Get(ctx, ref)
	require.ErrorIs(t, err, errdefs.ErrNotFound)
	deletedNames := make(map[string]bool)
	for range 2 {
		select {
		case envelope := <-events:
			require.NotNil(t, envelope)
			event, err := typeurl.UnmarshalAny(envelope.Event)
			require.NoError(t, err)
			name := event.(*eventstypes.ImageDelete).Name
			require.False(t, deletedNames[name], "delete event must not be duplicated")
			deletedNames[name] = true
			_, err = store.Get(ctx, name)
			require.ErrorIs(t, err, errdefs.ErrNotFound)
		case err := <-errs:
			t.Fatalf("delete event subscription failed: %v", err)
		}
	}
	require.Equal(t, map[string]bool{ref: true, id: true}, deletedNames)
}

func TestRemoveImageInvalidUnrelatedMetadata(t *testing.T) {
	ctx, c, cs := newImageEventService(t)
	config := cs.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{Platform: platforms.DefaultSpec(), RootFS: ocispec.RootFS{Type: "layers"}})
	manifest := cs.Manifest(config)
	ref := "example.com/test:latest"
	_, err := c.images.Create(ctx, ctrdimages.Image{Name: ref, Target: manifest.Descriptor})
	require.NoError(t, err)
	require.NoError(t, c.UpdateImage(ctx, ref))
	_, err = c.images.Create(ctx, ctrdimages.Image{Name: "example.com/broken:latest", Target: manifest.Descriptor,
		Labels: map[string]string{labels.ImagePlatformLabelKey: "invalid platform"}})
	require.NoError(t, err)
	broken := cs.Blob(ocispec.MediaTypeImageManifest, []byte("{"))
	_, err = c.images.Create(ctx, ctrdimages.Image{Name: "example.com/broken-manifest:latest", Target: broken.Descriptor})
	require.NoError(t, err)
	c.client = &pullTestClient{getImageWithPlatform: func(context.Context, string, platforms.MatchComparer) (containerd.Image, error) {
		t.Error("RemoveImage must use the listed image record without fetching the mutable reference again")
		return nil, errdefs.ErrUnavailable
	}}
	c.images = &imageEventStore{Store: c.images, beforeList: func(context.Context) { t.Error("removal must only read indexed references") }}
	_, err = c.images.Update(ctx, ctrdimages.Image{Name: ref, Labels: map[string]string{labels.ImagePlatformLabelKey: "invalid platform"}}, "labels."+labels.ImagePlatformLabelKey)
	require.NoError(t, err)
	require.Error(t, c.RemoveImage(ctx, &runtime.ImageSpec{Image: config.Descriptor.Digest.String()}))
	_, err = c.images.Update(ctx, ctrdimages.Image{Name: ref, Labels: map[string]string{labels.ImagePlatformLabelKey: ""}}, "labels."+labels.ImagePlatformLabelKey)
	require.NoError(t, err)
	require.NoError(t, cs.Delete(ctx, manifest.Descriptor.Digest))
	require.NoError(t, c.RemoveImage(ctx, &runtime.ImageSpec{Image: config.Descriptor.Digest.String()}))
	_, err = c.LocalResolve(ref, "")
	require.ErrorIs(t, err, errdefs.ErrNotFound)
	_, err = c.images.Get(ctx, ref)
	require.ErrorIs(t, err, errdefs.ErrNotFound)
	_, err = c.images.Get(ctx, "example.com/broken:latest")
	require.NoError(t, err)
	_, err = c.images.Get(ctx, "example.com/broken-manifest:latest")
	require.NoError(t, err)
}

func TestRemoveImageRetaggedPlatform(t *testing.T) {
	for _, test := range []struct {
		retag, incomplete bool
	}{
		{retag: true}, {}, {retag: true, incomplete: true}, {incomplete: true},
	} {
		t.Run(fmt.Sprintf("target=%t/incomplete=%t", test.retag, test.incomplete), func(t *testing.T) {
			ctx, c, cs := newImageEventService(t)
			guest := ocispec.Platform{OS: "windows", Architecture: "amd64", OSVersion: "10.0.20348"}
			c.runtimePlatforms["guest"] = &ImagePlatform{Platform: &guest}
			oldConfig := cs.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{Platform: guest, RootFS: ocispec.RootFS{Type: "layers"}})
			newConfig := cs.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{Platform: guest, RootFS: ocispec.RootFS{Type: "layers"}, Config: ocispec.ImageConfig{Env: []string{"VERSION=new"}}})
			hostConfig := cs.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{Platform: platforms.DefaultSpec(), RootFS: ocispec.RootFS{Type: "layers"}})
			oldManifest := cs.Manifest(oldConfig)
			guestManifest, hostManifest := cs.Manifest(newConfig), cs.Manifest(hostConfig)
			guestManifest.Descriptor.Platform = &guest
			host := platforms.DefaultSpec()
			hostManifest.Descriptor.Platform = &host
			index := cs.Index(hostManifest, guestManifest)
			ref := "example.com/test:latest"
			name := imagestore.Name(ref, "guest")
			initial := ctrdimages.Image{Name: name, Target: oldManifest.Descriptor, Labels: map[string]string{labels.ImagePlatformLabelKey: platforms.FormatAll(guest)}}
			removedID := oldConfig.Descriptor.Digest.String()
			if !test.retag {
				initial.Target = index.Descriptor
				initial.Labels[labels.ImagePlatformLabelKey] = platforms.FormatAll(host)
				removedID = hostConfig.Descriptor.Digest.String()
			}
			_, err := c.images.Create(ctx, initial)
			require.NoError(t, err)
			require.NoError(t, c.UpdateImage(ctx, name))
			_, err = c.images.Update(ctx, ctrdimages.Image{Name: name, Target: index.Descriptor, Labels: map[string]string{labels.ImagePlatformLabelKey: platforms.FormatAll(guest)}}, "target", "labels."+labels.ImagePlatformLabelKey)
			require.NoError(t, err)
			if test.incomplete {
				require.NoError(t, cs.Delete(ctx, guestManifest.Descriptor.Digest))
			}
			require.NoError(t, c.RemoveImage(ctx, &runtime.ImageSpec{Image: removedID, RuntimeHandler: "guest"}))
			current, err := c.images.Get(ctx, name)
			require.NoError(t, err)
			require.Equal(t, index.Descriptor.Digest, current.Target.Digest)
			require.Equal(t, platforms.FormatAll(guest), current.Labels[labels.ImagePlatformLabelKey])
			image, err := c.LocalResolve(ref, "guest")
			if test.incomplete {
				require.ErrorIs(t, err, errdefs.ErrNotFound)
			} else {
				require.NoError(t, err)
				require.Equal(t, newConfig.Descriptor.Digest.String(), image.ID)
			}
		})
	}
}

func TestTransferPullPreservesStoredTarget(t *testing.T) {
	platform := ocispec.Platform{
		OS:           "windows",
		Architecture: "amd64",
		OSVersion:    "10.0.20348",
		OSFeatures:   []string{"win32k"},
	}
	for _, mutation := range []string{"delete", "retag"} {
		t.Run(mutation, func(t *testing.T) {
			ctx, c, cs := newImageEventService(t)
			config := cs.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{Platform: platform, RootFS: ocispec.RootFS{Type: "layers"}})
			manifest := cs.Manifest(config)
			other := cs.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{Platform: platforms.DefaultSpec(), Config: ocispec.ImageConfig{Env: []string{"VERSION=other"}}, RootFS: ocispec.RootFS{Type: "layers"}})
			otherManifest := cs.Manifest(other)
			ref := imagestore.Name("example.com/test:latest", "native")
			c.transferrer = &pullTestTransferrer{transfer: func(ctx context.Context, _, destination any, _ ...transfer.Opt) error {
				store := destination.(*pullImageStore)
				encoded, err := store.MarshalAny(ctx, nil)
				require.NoError(t, err)
				var destinationSpec transfertypes.ImageStore
				require.NoError(t, typeurl.UnmarshalTo(encoded, &destinationSpec))
				require.Equal(t, ref, destinationSpec.Name)
				require.Equal(t, []ocispec.Platform{platform}, store.Platforms())
				require.Equal(t, platform, store.UnpackPlatforms()[0].Platform)
				require.Equal(t, "native", store.UnpackPlatforms()[0].Snapshotter)
				_, err = store.Store(ctx, manifest.Descriptor, c.images)
				require.NoError(t, err)
				if mutation == "delete" {
					return c.images.Delete(ctx, ref)
				}
				_, err = c.images.Update(ctx, ctrdimages.Image{Name: ref, Target: otherManifest.Descriptor}, "target")
				return err
			}}
			image, _, err := c.pullImageWithTransferService(ctx, "example.com/test:latest", ref, nil, "native", &platform, nil, time.Minute)
			require.NoError(t, err)
			require.True(t, image.Platform().Match(platform))
			require.Equal(t, manifest.Descriptor.Digest, image.Target().Digest)
		})
	}
}

func TestImageEventExternalMutation(t *testing.T) {
	t.Run("legacy conflict", func(t *testing.T) {
		ctx, c, cs := newImageEventService(t)
		c.defaultRuntimeName = "runc"
		ref := "example.com/test:latest"
		var ids []string
		for n, version := range []string{"handler", "legacy"} {
			config := cs.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{Platform: platforms.DefaultSpec(), RootFS: ocispec.RootFS{Type: "layers"}, Config: ocispec.ImageConfig{Env: []string{"VERSION=" + version}}})
			name := ref
			if n == 0 {
				name = imagestore.Name(ref, "runc")
			}
			_, err := c.images.Create(ctx, ctrdimages.Image{Name: name, Target: cs.Manifest(config).Descriptor})
			require.NoError(t, err)
			require.NoError(t, c.UpdateImage(ctx, name))
			ids = append(ids, config.Descriptor.Digest.String())
		}
		for _, recover := range []bool{false, true} {
			if recover {
				c.imageStore = imagestore.NewStore(c.images, cs.Store, platforms.Default())
				persisted, err := c.images.List(ctx)
				require.NoError(t, err)
				for _, image := range persisted {
					require.NoError(t, c.UpdateImage(ctx, image.Name))
				}
			}
			resolved, err := c.LocalResolve(ref, "")
			require.NoError(t, err)
			require.Equal(t, ids[0], resolved.ID)
			listed, err := c.listImages(nil)
			require.NoError(t, err)
			for _, image := range listed {
				if image.Id == ids[1] {
					require.NotContains(t, image.RepoTags, ref)
				}
			}
			legacy, err := c.LocalResolve(ids[1], "")
			require.NoError(t, err)
			require.Contains(t, legacy.References, ref)
			require.NotContains(t, c.toCRIImage(legacy).RepoTags, ref)
		}
		require.NoError(t, c.images.Delete(ctx, ids[1]))
		require.NoError(t, c.UpdateImage(ctx, ids[1]))
		legacy, err := c.LocalResolve(ids[1], "")
		require.NoError(t, err)
		require.Equal(t, []string{ref}, legacy.References)
		require.NoError(t, c.images.Delete(ctx, imagestore.Name(ref, "runc")))
		require.NoError(t, c.UpdateImage(ctx, imagestore.Name(ref, "runc")))
		resolved, err := c.LocalResolve(ref, "")
		require.NoError(t, err)
		require.Equal(t, ids[1], resolved.ID)
		require.Contains(t, resolved.References, ref)
		persisted, err := c.images.Get(ctx, ref)
		require.NoError(t, err)
		require.NoError(t, cs.Delete(ctx, persisted.Target.Digest))
		require.NoError(t, c.RemoveImage(ctx, &runtime.ImageSpec{Image: ids[1]}))
		_, err = c.images.Get(ctx, ref)
		require.ErrorIs(t, err, errdefs.ErrNotFound)
	})
	for _, test := range []struct {
		handler   string
		afterRead bool
	}{
		{},
		{afterRead: true},
		{handler: "runc", afterRead: true},
	} {
		for _, deleted := range []bool{false, true} {
			t.Run(fmt.Sprintf("handler=%s/delete=%t/afterRead=%t", test.handler, deleted, test.afterRead), func(t *testing.T) {
				ctx, c, cs := newImageEventService(t)
				config := cs.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{Platform: platforms.DefaultSpec(), RootFS: ocispec.RootFS{Type: "layers"}})
				newConfig := cs.JSONObject(ocispec.MediaTypeImageConfig, ocispec.Image{Platform: platforms.DefaultSpec(), RootFS: ocispec.RootFS{Type: "layers"}, Config: ocispec.ImageConfig{Env: []string{"VERSION=new"}}})
				newManifest := cs.Manifest(newConfig)
				ref := imagestore.Name("example.com/test:latest", test.handler)
				id := imagestore.Name(config.Descriptor.Digest.String(), test.handler)
				_, err := c.images.Create(ctx, ctrdimages.Image{Name: ref, Target: cs.Manifest(config).Descriptor, Labels: map[string]string{"external": "keep"}})
				require.NoError(t, err)
				store := &imageEventStore{Store: c.images}
				mutate := func(ctx context.Context) {
					if deleted {
						require.NoError(t, store.Store.Delete(ctx, ref))
					} else {
						_, err := store.Store.Update(ctx, ctrdimages.Image{Name: ref, Target: newManifest.Descriptor}, "target")
						require.NoError(t, err)
					}
				}
				if test.afterRead {
					c.defaultRuntimeName = "runc"
					require.NoError(t, c.UpdateImage(ctx, ref))
					store.afterGet = func(ctx context.Context, name string) {
						require.Equal(t, ref, name)
						require.NoError(t, store.Store.Delete(ctx, id))
						mutate(ctx)
					}
				} else {
					store.beforeUpdate = mutate
				}
				c.images = store
				require.NoError(t, c.UpdateImage(ctx, ref))
				if test.afterRead {
					_, err = store.Get(ctx, id)
					require.ErrorIs(t, err, errdefs.ErrNotFound, "stale event must not recreate the old config-ID reference")
					// Process the external mutation notifications after the older event.
					require.NoError(t, c.UpdateImage(ctx, ref))
					require.NoError(t, c.UpdateImage(ctx, id))
				}
				listed, err := c.listImages(nil)
				require.NoError(t, err)
				if deleted {
					require.Empty(t, listed)
					persisted, err := store.List(ctx)
					require.NoError(t, err)
					require.Empty(t, persisted)
					return
				}
				require.Len(t, listed, 1)
				require.Equal(t, newConfig.Descriptor.Digest.String(), listed[0].Id)
				_, err = store.Get(ctx, imagestore.Name(listed[0].Id, test.handler))
				require.NoError(t, err)
				stored, err := store.Get(ctx, ref)
				require.NoError(t, err)
				assert.Equal(t, newManifest.Descriptor, stored.Target)
				assert.Equal(t, "keep", stored.Labels["external"])
				resolved, err := c.LocalResolve("example.com/test:latest", test.handler)
				require.NoError(t, err)
				assert.Equal(t, newConfig.Descriptor.Digest.String(), resolved.ID)
				updates := store.updates
				require.NoError(t, c.UpdateImage(ctx, ref))
				assert.Equal(t, updates, store.updates, "unchanged events must not publish another update")
				require.NoError(t, store.Delete(ctx, ref))
				require.NoError(t, c.UpdateImage(ctx, ref))
				_, err = c.LocalResolve("example.com/test:latest", test.handler)
				assert.ErrorIs(t, err, errdefs.ErrNotFound)
			})
		}
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
				activeReqs, totalBytesRead, _ := r.reqReporter.status()
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
				activeReqs, totalBytesRead, _ := r.reqReporter.status()
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
				activeReqs, totalBytesRead, _ := r.reqReporter.status()
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
				activeReqs, totalBytesRead, _ := r.reqReporter.status()
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

	t.Run("ZeroBodyResponseFollowedByRequestIsNotCancelled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
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

			client := &http.Client{
				Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					switch req.URL.Path {
					case "/token":
						time.Sleep(190 * time.Millisecond)
						return &http.Response{
							StatusCode: http.StatusOK,
							Body:       io.NopCloser(strings.NewReader("token")),
						}, nil
					case "/v2/test/manifests/latest":
						time.Sleep(160 * time.Millisecond)
						return &http.Response{
							StatusCode: http.StatusOK,
							Body:       http.NoBody,
						}, nil
					case "/v2/test/manifests/sha256:1234":
						time.Sleep(160 * time.Millisecond)
						return &http.Response{
							StatusCode: http.StatusOK,
							Body:       io.NopCloser(strings.NewReader("manifest")),
						}, nil
					default:
						t.Fatalf("unexpected request path: %s", req.URL.Path)
						return nil, nil
					}
				}),
			}
			assert.NoError(t, reporter.optionUpdateClient(client))
			reporter.start(ctx)

			doReq := func(method, url string) {
				req, err := http.NewRequestWithContext(ctx, method, url, nil)
				require.NoError(t, err)
				resp, err := client.Do(req)
				require.NoError(t, err)
				_, err = io.Copy(io.Discard, resp.Body)
				assert.NoError(t, err)
				assert.NoError(t, resp.Body.Close())
			}

			doReq(http.MethodGet, "https://example.com/token")
			doReq(http.MethodHead, "https://example.com/v2/test/manifests/latest")
			doReq(http.MethodGet, "https://example.com/v2/test/manifests/sha256:1234")

			select {
			case <-cancelCalled:
				t.Fatal("pull was cancelled during active GET following a 0-byte HEAD response")
			default:
			}
		})
	})

	t.Run("RequestAfterMissedIdleTransitionIsNotCancelled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
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

			// With a 200ms timeout, the reporter ticks every 100ms. Each
			// request finishes within 200ms, and the idle periods fall
			// between ticks.
			reporter.reqReporter.incRequest()
			reporter.reqReporter.incByteRead(1024)
			reporter.start(ctx)

			time.Sleep(120 * time.Millisecond)
			reporter.reqReporter.decRequest()

			time.Sleep(60 * time.Millisecond)
			reporter.reqReporter.incRequest()
			time.Sleep(90 * time.Millisecond)
			reporter.reqReporter.decRequest()

			time.Sleep(20 * time.Millisecond)
			reporter.reqReporter.incRequest()

			select {
			case <-cancelCalled:
				t.Fatal("pull was cancelled before any request exceeded timeout")
			case <-time.After(130 * time.Millisecond):
			}
		})
	})
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type pullTestClient struct {
	listImages           func(context.Context, ...string) ([]containerd.Image, error)
	pull                 func(context.Context, string, ...containerd.RemoteOpt) (containerd.Image, error)
	getImageWithPlatform func(context.Context, string, platforms.MatchComparer) (containerd.Image, error)
	imageFromRecord      func(ctrdimages.Image, platforms.MatchComparer) containerd.Image
}

func (c *pullTestClient) ListImages(ctx context.Context, filters ...string) ([]containerd.Image, error) {
	if c.listImages != nil {
		return c.listImages(ctx, filters...)
	}
	return nil, nil
}

func (c *pullTestClient) GetImageWithPlatform(ctx context.Context, ref string, platform platforms.MatchComparer) (containerd.Image, error) {
	return c.getImageWithPlatform(ctx, ref, platform)
}

func (c *pullTestClient) ImageFromRecord(image ctrdimages.Image, platform platforms.MatchComparer) containerd.Image {
	return c.imageFromRecord(image, platform)
}

func (c *pullTestClient) Pull(ctx context.Context, ref string, opts ...containerd.RemoteOpt) (containerd.Image, error) {
	return c.pull(ctx, ref, opts...)
}

func (c *pullTestClient) WithLease(ctx context.Context, _ ...leases.Opt) (context.Context, func(context.Context) error, error) {
	return ctx, func(context.Context) error { return nil }, nil
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
