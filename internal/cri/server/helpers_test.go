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
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	runcoptions "github.com/containerd/containerd/api/types/runc/options"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/imagetest"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/internal/cri/annotations"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/containerd/v2/pkg/protobuf/types"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	bolt "go.etcd.io/bbolt"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToContainerdImage(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	cs := imagetest.NewContentStore(ctx, t)
	db, err := bolt.Open(filepath.Join(t.TempDir(), "metadata.db"), 0o600, nil)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	store := metadata.NewImageStore(metadata.NewDB(db, cs.Store, nil))
	client, err := containerd.New("", containerd.WithServices(containerd.WithImageStore(store), containerd.WithContentStore(cs.Store)),
		containerd.WithDefaultPlatform(platforms.OnlyStrict(imagespec.Platform{OS: "linux", Architecture: "amd64"})))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	var configs, manifests []imagetest.Content
	for _, arch := range []string{"amd64", "arm64"} {
		platform := imagespec.Platform{OS: "linux", Architecture: arch}
		config := cs.JSONObject(imagespec.MediaTypeImageConfig, imagespec.Image{Platform: platform,
			RootFS: imagespec.RootFS{Type: "layers", DiffIDs: []digest.Digest{digest.FromString(arch)}}})
		configs = append(configs, config)
		manifests = append(manifests, imagetest.AddPlatform(cs.Manifest(config), platform))
	}
	index := cs.Index(append([]imagetest.Content{{Descriptor: imagespec.Descriptor{MediaType: imagespec.MediaTypeImageIndex,
		Digest: digest.FromString("missing-index"), Platform: &imagespec.Platform{OS: "windows", Architecture: "amd64"}}}}, manifests...)...)
	for _, handler := range []string{"", "runc"} {
		t.Run(handler, func(t *testing.T) {
			name := imagestore.Name("example.com/image:latest", handler)
			selected := imagestore.Image{ID: configs[1].Descriptor.Digest.String(), RuntimeHandler: handler}
			c := &criService{client: client}
			stableID := imagestore.Name(selected.ID, handler)
			repoDigest := imagestore.Name("example.com/image@"+index.Descriptor.Digest.String(), handler)
			selected.References = []string{name, stableID, repoDigest}
			for _, ref := range selected.References {
				_, err := store.Create(ctx, images.Image{Name: ref, Target: index.Descriptor})
				require.NoError(t, err)
			}
			for _, mutation := range []struct {
				ref     string
				deleted bool
			}{
				{ref: name},
				{ref: name, deleted: true},
				{ref: stableID},
			} {
				if mutation.deleted {
					require.NoError(t, store.Delete(ctx, mutation.ref))
				} else {
					_, err := store.Update(ctx, images.Image{Name: mutation.ref, Target: manifests[0].Descriptor}, "target")
					require.NoError(t, err)
				}
				converted, originalDigest, err := c.toContainerdImage(ctx, selected)
				require.NoError(t, err)
				config, err := converted.Config(ctx)
				require.NoError(t, err)
				assert.Equal(t, selected.ID, config.Digest.String())
				assert.Equal(t, index.Descriptor.Digest.String(), originalDigest)
				assert.Equal(t, manifests[1].Descriptor, converted.Target())
			}
			selected.ID = digest.FromString("stale").String()
			_, _, err = c.toContainerdImage(ctx, selected)
			require.ErrorIs(t, err, errdefs.ErrNotFound)
		})
	}
}

func TestEffectiveSandboxRuntimeHandler(t *testing.T) {
	c := newTestCRIService()
	c.config.DefaultRuntimeName = "default"
	c.config.Runtimes = map[string]criconfig.Runtime{
		"default":                  {},
		"alternate":                {},
		criconfig.RuntimeUntrusted: {},
	}
	for _, test := range []struct {
		name      string
		handler   string
		untrusted bool
		expected  string
		expectErr bool
	}{
		{name: "legacy default", expected: "default"},
		{name: "explicit default", handler: "default", expected: "default"},
		{name: "alternate", handler: "alternate", expected: "alternate"},
		{name: "implicit untrusted", untrusted: true, expected: criconfig.RuntimeUntrusted},
		{name: "explicit untrusted", handler: criconfig.RuntimeUntrusted, untrusted: true, expected: criconfig.RuntimeUntrusted},
		{name: "untrusted rejects explicit default", handler: "default", untrusted: true, expectErr: true},
		{name: "unknown", handler: "unknown", expectErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := &runtime.PodSandboxConfig{}
			if test.untrusted {
				config.Annotations = map[string]string{annotations.UntrustedWorkload: "true"}
			}
			handler, err := c.effectiveSandboxRuntimeHandler(config, test.handler)
			if test.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.expected, handler)
		})
	}
}

// TestGetUserFromImage tests the logic of getting image uid or user name of image user.
func TestGetUserFromImage(t *testing.T) {
	newI64 := func(i int64) *int64 { return &i }
	for _, test := range []struct {
		desc string
		user string
		uid  *int64
		name string
	}{
		{
			desc: "no gid",
			user: "0",
			uid:  newI64(0),
		},
		{
			desc: "uid/gid",
			user: "0:1",
			uid:  newI64(0),
		},
		{
			desc: "empty user",
			user: "",
		},
		{
			desc: "multiple separators",
			user: "1:2:3",
			uid:  newI64(1),
		},
		{
			desc: "root username",
			user: "root:root",
			name: "root",
		},
		{
			desc: "username",
			user: "test:test",
			name: "test",
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			actualUID, actualName := getUserFromImage(test.user)
			assert.Equal(t, test.uid, actualUID)
			assert.Equal(t, test.name, actualName)
		})
	}
}

func TestGenerateRuntimeOptions(t *testing.T) {
	nilOpts := `
systemd_cgroup = true
[containerd]
  no_pivot = true
  default_runtime_name = "default"
[containerd.runtimes.runcv2]
  runtime_type = "` + plugins.RuntimeRuncV2 + `"
`
	nonNilOpts := `
systemd_cgroup = true
[containerd]
  no_pivot = true
  default_runtime_name = "default"
[containerd.runtimes.legacy.options]
  Runtime = "legacy"
  RuntimeRoot = "/legacy"
[containerd.runtimes.runc.options]
  BinaryName = "runc"
  Root = "/runc"
  NoNewKeyring = true
[containerd.runtimes.runcv2]
  runtime_type = "` + plugins.RuntimeRuncV2 + `"
[containerd.runtimes.runcv2.options]
  BinaryName = "runc"
  Root = "/runcv2"
  NoNewKeyring = true
`
	var nilOptsConfig, nonNilOptsConfig criconfig.Config
	err := toml.Unmarshal([]byte(nilOpts), &nilOptsConfig)
	require.NoError(t, err)
	require.Len(t, nilOptsConfig.Runtimes, 1)

	err = toml.Unmarshal([]byte(nonNilOpts), &nonNilOptsConfig)
	require.NoError(t, err)
	require.Len(t, nonNilOptsConfig.Runtimes, 3)

	for _, test := range []struct {
		desc            string
		r               criconfig.Runtime
		c               criconfig.Config
		expectedOptions any
	}{
		{
			desc:            "when options is nil, should return nil option for io.containerd.runc.v2",
			r:               nilOptsConfig.Runtimes["runcv2"],
			c:               nilOptsConfig,
			expectedOptions: nil,
		},
		{
			desc: "when options is not nil, should be able to decode for io.containerd.runc.v2",
			r:    nonNilOptsConfig.Runtimes["runcv2"],
			c:    nonNilOptsConfig,
			expectedOptions: &runcoptions.Options{
				BinaryName:   "runc",
				Root:         "/runcv2",
				NoNewKeyring: true,
			},
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			opts, err := criconfig.GenerateRuntimeOptions(test.r)
			assert.NoError(t, err)
			assert.Equal(t, test.expectedOptions, opts)
		})
	}
}

func TestEnvDeduplication(t *testing.T) {
	for _, test := range []struct {
		desc     string
		existing []string
		kv       [][2]string
		expected []string
	}{
		{
			desc: "single env",
			kv: [][2]string{
				{"a", "b"},
			},
			expected: []string{"a=b"},
		},
		{
			desc: "multiple envs",
			kv: [][2]string{
				{"a", "b"},
				{"c", "d"},
				{"e", "f"},
			},
			expected: []string{
				"a=b",
				"c=d",
				"e=f",
			},
		},
		{
			desc: "env override",
			kv: [][2]string{
				{"k1", "v1"},
				{"k2", "v2"},
				{"k3", "v3"},
				{"k3", "v4"},
				{"k1", "v5"},
				{"k4", "v6"},
			},
			expected: []string{
				"k1=v5",
				"k2=v2",
				"k3=v4",
				"k4=v6",
			},
		},
		{
			desc: "existing env",
			existing: []string{
				"k1=v1",
				"k2=v2",
				"k3=v3",
			},
			kv: [][2]string{
				{"k3", "v4"},
				{"k2", "v5"},
				{"k4", "v6"},
			},
			expected: []string{
				"k1=v1",
				"k2=v5",
				"k3=v4",
				"k4=v6",
			},
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			var spec runtimespec.Spec
			if len(test.existing) > 0 {
				spec.Process = &runtimespec.Process{
					Env: test.existing,
				}
			}
			for _, kv := range test.kv {
				oci.WithEnv([]string{kv[0] + "=" + kv[1]})(context.Background(), nil, nil, &spec)
			}
			assert.Equal(t, test.expected, spec.Process.Env)
		})
	}
}

func TestEnsureRemoveAllNotExist(t *testing.T) {
	// should never return an error for a non-existent path
	if err := ensureRemoveAll(context.Background(), "/non/existent/path"); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureRemoveAllWithDir(t *testing.T) {
	dir := t.TempDir()
	if err := ensureRemoveAll(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureRemoveAllWithFile(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "test-ensure-removeall-with-dir")
	require.NoError(t, err)
	tmp.Close()

	err = ensureRemoveAll(context.Background(), tmp.Name())
	require.NoError(t, err)
}

// Helper function for setting up an environment to test PID namespace targeting.
func addContainer(c *criService, containerID, sandboxID string, PID uint32, createdAt, startedAt, finishedAt int64) error {
	meta := containerstore.Metadata{
		ID:        containerID,
		SandboxID: sandboxID,
	}
	status := containerstore.Status{
		Pid:        PID,
		CreatedAt:  createdAt,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	}
	container, err := containerstore.NewContainer(meta,
		containerstore.WithFakeStatus(status),
	)
	if err != nil {
		return err
	}
	return c.containerStore.Add(container)
}

func TestValidateTargetContainer(t *testing.T) {
	testSandboxID := "test-sandbox-uid"

	// The existing container that will be targeted.
	testTargetContainerID := "test-target-container"
	testTargetContainerPID := uint32(4567)

	// A container that has finished running and cannot be targeted.
	testStoppedContainerID := "stopped-target-container"
	testStoppedContainerPID := uint32(6789)

	// A container from another pod.
	testOtherContainerSandboxID := "other-sandbox-uid"
	testOtherContainerID := "other-target-container"
	testOtherContainerPID := uint32(7890)

	// Container create/start/stop times.
	createdAt := time.Now().Add(-15 * time.Second).UnixNano()
	startedAt := time.Now().Add(-10 * time.Second).UnixNano()
	finishedAt := time.Now().Add(-5 * time.Second).UnixNano()

	c := newTestCRIService()

	// Create a target container.
	err := addContainer(c, testTargetContainerID, testSandboxID, testTargetContainerPID, createdAt, startedAt, 0)
	require.NoError(t, err, "error creating test target container")

	// Create a stopped container.
	err = addContainer(c, testStoppedContainerID, testSandboxID, testStoppedContainerPID, createdAt, startedAt, finishedAt)
	require.NoError(t, err, "error creating test stopped container")

	// Create a container in another pod.
	err = addContainer(c, testOtherContainerID, testOtherContainerSandboxID, testOtherContainerPID, createdAt, startedAt, 0)
	require.NoError(t, err, "error creating test container in other pod")

	for _, test := range []struct {
		desc              string
		targetContainerID string
		expectError       bool
	}{
		{
			desc:              "target container in pod",
			targetContainerID: testTargetContainerID,
			expectError:       false,
		},
		{
			desc:              "target stopped container in pod",
			targetContainerID: testStoppedContainerID,
			expectError:       true,
		},
		{
			desc:              "target container does not exist",
			targetContainerID: "no-container-with-this-id",
			expectError:       true,
		},
		{
			desc:              "target container in other pod",
			targetContainerID: testOtherContainerID,
			expectError:       true,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			targetContainer, err := c.validateTargetContainer(testSandboxID, test.targetContainerID)
			if test.expectError {
				require.Error(t, err, "target should have been invalid but no error")
				return
			}
			require.NoErrorf(t, err, "target should have been valid but got error")

			assert.Equal(t, test.targetContainerID, targetContainer.ID, "returned target container does not have expected ID")
		})
	}

}

func TestGetRuntimeOptions(t *testing.T) {
	_, err := getRuntimeOptions(containers.Container{})
	require.NoError(t, err)

	var pbany *types.Any               // This is nil.
	var typeurlAny typeurl.Any = pbany // This is typed nil.
	_, err = getRuntimeOptions(containers.Container{Runtime: containers.RuntimeInfo{Options: typeurlAny}})
	require.NoError(t, err)
}

func TestCopyResourcesToStatusWindowsAffinity(t *testing.T) {
	spec := &runtimespec.Spec{
		Windows: &runtimespec.Windows{
			Resources: &runtimespec.WindowsResources{
				CPU: &runtimespec.WindowsCPUResources{
					Affinity: []runtimespec.WindowsCPUGroupAffinity{
						{Mask: 0x3, Group: 0},
						{Mask: 0xf, Group: 1},
					},
				},
			},
		},
	}
	status := copyResourcesToStatus(spec, containerstore.Status{})
	require.NotNil(t, status.Resources)
	require.NotNil(t, status.Resources.Windows)
	assert.Equal(t, []*runtime.WindowsCpuGroupAffinity{
		{CpuMask: 0x3, CpuGroup: 0},
		{CpuMask: 0xf, CpuGroup: 1},
	}, status.Resources.Windows.AffinityCpus)
}

func TestHostNetwork(t *testing.T) {
	tests := []struct {
		name     string
		c        *runtime.PodSandboxConfig
		expected bool
	}{
		{
			name: "when pod namespace return false",
			c: &runtime.PodSandboxConfig{
				Linux: &runtime.LinuxPodSandboxConfig{
					SecurityContext: &runtime.LinuxSandboxSecurityContext{
						NamespaceOptions: &runtime.NamespaceOption{
							Network: runtime.NamespaceMode_POD,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "when node namespace return true",
			c: &runtime.PodSandboxConfig{
				Linux: &runtime.LinuxPodSandboxConfig{
					SecurityContext: &runtime.LinuxSandboxSecurityContext{
						NamespaceOptions: &runtime.NamespaceOption{
							Network: runtime.NamespaceMode_NODE,
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		if goruntime.GOOS != "linux" {
			t.Skip()
		}

		t.Run(tt.name, func(t *testing.T) {
			if hostNetwork(tt.c) != tt.expected {
				t.Errorf("failed hostNetwork got %t expected %t", hostNetwork(tt.c), tt.expected)
			}
		})
	}
}
